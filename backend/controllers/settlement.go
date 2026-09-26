package controllers

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"edu-train/database"
	"edu-train/models"
	"edu-train/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var monthRegexp = regexp.MustCompile(`^\d{4}-\d{2}$`)

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// buildSettlementItems 查询某位教师某月份所有已点名（已完成且存在考勤记录）的课，
// 按当前课时费标准计算每节课的费用，返回明细与合计
func buildSettlementItems(tx *gorm.DB, teacherID uint, month string, hourlyRate float64) ([]models.TeacherSettlementItem, int, float64) {
	var schedules []models.Schedule
	tx.Preload("Course").
		Where("teacher_id = ? AND status = ? AND date LIKE ?", teacherID, "completed", month+"-%").
		Where("EXISTS (SELECT 1 FROM attendances WHERE attendances.schedule_id = schedules.id AND attendances.deleted_at IS NULL)").
		Order("date ASC, start_time ASC").
		Find(&schedules)

	items := make([]models.TeacherSettlementItem, 0, len(schedules))
	totalHours := 0
	totalSalary := 0.0
	for _, s := range schedules {
		amount := round2(float64(s.Duration) * hourlyRate)
		courseName := ""
		if s.Course != nil {
			courseName = s.Course.Name
		}
		items = append(items, models.TeacherSettlementItem{
			ScheduleID: s.ID,
			CourseID:   s.CourseID,
			CourseName: courseName,
			Date:       s.Date,
			StartTime:  s.StartTime,
			EndTime:    s.EndTime,
			Duration:   s.Duration,
			HourlyRate: hourlyRate,
			Amount:     amount,
		})
		totalHours += s.Duration
		totalSalary += amount
	}
	return items, totalHours, round2(totalSalary)
}

// GetTeacherSettlements 结算单列表
func GetTeacherSettlements(c *gin.Context) {
	teacherID := c.Query("teacher_id")
	month := c.Query("month")
	status := c.Query("status")

	query := database.DB.Model(&models.TeacherSettlement{}).Preload("Teacher")

	if teacherID != "" {
		query = query.Where("teacher_id = ?", teacherID)
	}
	if month != "" {
		query = query.Where("month = ?", month)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var settlements []models.TeacherSettlement
	if err := query.Order("month DESC, teacher_id ASC").Find(&settlements).Error; err != nil {
		utils.InternalServerError(c, "查询失败")
		return
	}

	utils.Success(c, settlements)
}

// GetTeacherSettlement 结算单详情（含每节课明细）
func GetTeacherSettlement(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	var settlement models.TeacherSettlement
	if err := database.DB.Preload("Teacher").Preload("Items").First(&settlement, id).Error; err != nil {
		utils.NotFound(c, "结算单不存在")
		return
	}

	utils.Success(c, settlement)
}

// GenerateTeacherSettlement 按月份生成（或重新生成）结算单。
// 同一教师同一月份只有一张单：未确认的直接按最新考勤重算；
// 已确认的金额锁定，仅当最新考勤算出的金额不同才标记为待重算。
// 不传 teacher_id 时为该月所有有已点名课程的教师生成。
func GenerateTeacherSettlement(c *gin.Context) {
	var req struct {
		Month     string `json:"month" binding:"required"`
		TeacherID *uint  `json:"teacher_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.BadRequest(c, "月份不能为空")
		return
	}

	req.Month = strings.TrimSpace(req.Month)
	if len(req.Month) >= 7 {
		req.Month = req.Month[:7]
	}
	if !monthRegexp.MatchString(req.Month) {
		utils.BadRequest(c, "月份格式应为 YYYY-MM")
		return
	}

	var teacherIDs []uint
	if req.TeacherID != nil {
		var teacher models.Teacher
		if err := database.DB.First(&teacher, *req.TeacherID).Error; err != nil {
			utils.NotFound(c, "教师不存在")
			return
		}
		teacherIDs = []uint{teacher.ID}
	} else {
		// 只给当月存在已点名课程的教师生成
		if err := database.DB.Model(&models.Schedule{}).
			Where("status = ? AND date LIKE ?", "completed", req.Month+"-%").
			Where("EXISTS (SELECT 1 FROM attendances WHERE attendances.schedule_id = schedules.id AND attendances.deleted_at IS NULL)").
			Distinct().Pluck("teacher_id", &teacherIDs).Error; err != nil {
			utils.InternalServerError(c, "查询教师失败")
			return
		}
	}

	generated := 0
	locked := 0

	for _, teacherID := range teacherIDs {
		var teacher models.Teacher
		if err := database.DB.First(&teacher, teacherID).Error; err != nil {
			continue
		}

		err := database.DB.Transaction(func(tx *gorm.DB) error {
			var settlement models.TeacherSettlement
			err := tx.Where("teacher_id = ? AND month = ?", teacherID, req.Month).
				Limit(1).Find(&settlement).Error

			if settlement.ID == 0 {
				items, totalHours, totalSalary := buildSettlementItems(tx, teacherID, req.Month, teacher.HourlyRate)
				settlement = models.TeacherSettlement{
					TeacherID:   teacherID,
					Month:       req.Month,
					TotalHours:  totalHours,
					TotalSalary: totalSalary,
					Status:      "pending",
					Items:       items,
				}
				if err := tx.Create(&settlement).Error; err != nil {
					return err
				}
				generated++
				return nil
			}
			if err != nil {
				return err
			}

			// 财务确认后金额锁定：confirmed / needs_recalc 都不会被重新生成覆盖，
			// confirmed 若考勤有变化则标记为待重算；needs_recalc 已等待财务手动重开
			if settlement.Status == "confirmed" || settlement.Status == "needs_recalc" {
				if settlement.Status == "confirmed" {
					_, totalHours, totalSalary := buildSettlementItems(tx, teacherID, req.Month, teacher.HourlyRate)
					if totalHours != settlement.TotalHours || totalSalary != settlement.TotalSalary {
						if err := tx.Model(&settlement).Update("status", "needs_recalc").Error; err != nil {
							return err
						}
					}
				}
				locked++
				return nil
			}

			// 待确认：按最新考勤重新计算
			items, totalHours, totalSalary := buildSettlementItems(tx, teacherID, req.Month, teacher.HourlyRate)

			if err := tx.Where("settlement_id = ?", settlement.ID).
				Delete(&models.TeacherSettlementItem{}).Error; err != nil {
				return err
			}
			for i := range items {
				items[i].SettlementID = settlement.ID
			}
			if len(items) > 0 {
				if err := tx.Create(&items).Error; err != nil {
					return err
				}
			}
			if err := tx.Model(&settlement).Updates(map[string]interface{}{
				"total_hours":  totalHours,
				"total_salary": totalSalary,
				"status":       "pending",
			}).Error; err != nil {
				return err
			}
			generated++
			return nil
		})
		if err != nil {
			utils.InternalServerError(c, "生成结算单失败")
			return
		}
	}

	utils.SuccessWithMessage(c,
		"生成完成：新增/重算 "+strconv.Itoa(generated)+" 张，"+strconv.Itoa(locked)+" 张已确认被锁定（如有变化已标记待重算）",
		gin.H{"generated": generated, "locked": locked})
}

// ConfirmTeacherSettlement 财务确认结算单，锁定金额
func ConfirmTeacherSettlement(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	var settlement models.TeacherSettlement
	if err := database.DB.First(&settlement, id).Error; err != nil {
		utils.NotFound(c, "结算单不存在")
		return
	}

	if settlement.Status == "confirmed" {
		utils.BadRequest(c, "结算单已确认，请勿重复操作")
		return
	}
	if settlement.Status == "needs_recalc" {
		utils.BadRequest(c, "该单考勤有更新，请先重新计算后再确认")
		return
	}

	now := time.Now()
	if err := database.DB.Model(&settlement).Updates(map[string]interface{}{
		"status":       "confirmed",
		"confirmed_at": &now,
	}).Error; err != nil {
		utils.InternalServerError(c, "确认失败")
		return
	}

	utils.SuccessWithMessage(c, "已确认，金额已锁定", nil)
}

// RecalculateTeacherSettlement 财务对已锁定（待重算）的单子手动重开：
// 按最新考勤重新计算并回到待确认状态
func RecalculateTeacherSettlement(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	var settlement models.TeacherSettlement
	if err := database.DB.First(&settlement, id).Error; err != nil {
		utils.NotFound(c, "结算单不存在")
		return
	}

	var teacher models.Teacher
	if err := database.DB.First(&teacher, settlement.TeacherID).Error; err != nil {
		utils.NotFound(c, "教师不存在")
		return
	}

	err := database.DB.Transaction(func(tx *gorm.DB) error {
		items, totalHours, totalSalary := buildSettlementItems(tx, settlement.TeacherID, settlement.Month, teacher.HourlyRate)

		if err := tx.Where("settlement_id = ?", settlement.ID).
			Delete(&models.TeacherSettlementItem{}).Error; err != nil {
			return err
		}
		for i := range items {
			items[i].SettlementID = settlement.ID
		}
		if len(items) > 0 {
			if err := tx.Create(&items).Error; err != nil {
				return err
			}
		}
		return tx.Model(&settlement).Updates(map[string]interface{}{
			"total_hours":  totalHours,
			"total_salary": totalSalary,
			"status":       "pending",
			"confirmed_at": nil,
		}).Error
	})
	if err != nil {
		utils.InternalServerError(c, "重算失败")
		return
	}

	utils.SuccessWithMessage(c, "已按最新考勤重算", nil)
}

// markSettlementNeedsRecalc 点名后调用：若该教师对应月份存在已确认的结算单，
// 因金额已锁定，只能把它标记为待重算（金额不变）
func markSettlementNeedsRecalc(tx *gorm.DB, teacherID uint, date string) error {
	if len(date) < 7 {
		return nil
	}
	month := date[:7]
	return tx.Model(&models.TeacherSettlement{}).
		Where("teacher_id = ? AND month = ? AND status = ?", teacherID, month, "confirmed").
		Update("status", "needs_recalc").Error
}
