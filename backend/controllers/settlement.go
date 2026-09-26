package controllers

import (
	"math"
	"strconv"
	"time"

	"edu-train/database"
	"edu-train/models"
	"edu-train/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	SettlementStatusPending   = "pending"   // 待确认
	SettlementStatusConfirmed = "confirmed" // 财务已确认，金额锁定
	SettlementStatusStale     = "stale"     // 确认后考勤有变动，待重算
)

// 结算单状态流转说明：
// pending --财务确认--> confirmed --确认后补录/改动考勤--> stale --重开重算--> pending（金额刷新）
// pending 状态可通过"按月生成"反复按最新考勤重算；
// confirmed/stale 的金额不会被批量生成覆盖，需显式"重开重算"。

type settlementLesson struct {
	ScheduleID uint
	CourseID   uint
	Date       string
	StartTime  string
	EndTime    string
	Duration   float64
	CourseName string
}

// round2 金额保留两位小数，避免浮点误差
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// listAttendedLessons 查询某位教师某个月已经点过名（考勤记录存在）的课
func listAttendedLessons(teacherID uint, month string) ([]settlementLesson, error) {
	var rows []settlementLesson
	err := database.DB.Table("schedules").
		Select("schedules.id AS schedule_id, schedules.course_id, schedules.date, schedules.start_time, schedules.end_time, schedules.duration, courses.name AS course_name").
		Joins("JOIN attendances ON attendances.schedule_id = schedules.id AND attendances.deleted_at IS NULL").
		Joins("LEFT JOIN courses ON courses.id = schedules.course_id AND courses.deleted_at IS NULL").
		Where("schedules.deleted_at IS NULL").
		Where("schedules.teacher_id = ?", teacherID).
		Where("schedules.date LIKE ?", month+"-%").
		Group("schedules.id").
		Order("schedules.date ASC, schedules.start_time ASC").
		Scan(&rows).Error
	return rows, err
}

// rebuildSettlement 按最新考勤重算一张结算单
// （批量生成只应作用于 pending；confirmed/stale 仅由"重开重算"显式调用）
func rebuildSettlement(tx *gorm.DB, teacher models.Teacher, month string) (models.TeacherSettlement, error) {
	var settlement models.TeacherSettlement
	err := tx.Where("teacher_id = ? AND month = ?", teacher.ID, month).First(&settlement).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return settlement, err
	}

	lessons, err := listAttendedLessons(teacher.ID, month)
	if err != nil {
		return settlement, err
	}

	totalHours := 0.0
	totalAmount := 0.0
	items := make([]models.SettlementItem, 0, len(lessons))
	for _, l := range lessons {
		amount := round2(l.Duration * teacher.HourlyRate)
		totalHours += l.Duration
		totalAmount += amount
		items = append(items, models.SettlementItem{
			ScheduleID: l.ScheduleID,
			CourseID:   l.CourseID,
			Date:       l.Date,
			StartTime:  l.StartTime,
			EndTime:    l.EndTime,
			CourseName: l.CourseName,
			Duration:   l.Duration,
			HourlyRate: teacher.HourlyRate,
			Amount:     amount,
		})
	}
	totalHours = round2(totalHours)
	totalAmount = round2(totalAmount)

	if settlement.ID == 0 {
		settlement = models.TeacherSettlement{
			TeacherID:   teacher.ID,
			Month:       month,
			HourlyRate:  teacher.HourlyRate,
			TotalHours:  totalHours,
			TotalAmount: totalAmount,
			Status:      SettlementStatusPending,
		}
		if err := tx.Create(&settlement).Error; err != nil {
			return settlement, err
		}
	} else {
		settlement.HourlyRate = teacher.HourlyRate
		settlement.TotalHours = totalHours
		settlement.TotalAmount = totalAmount
		settlement.Status = SettlementStatusPending
		settlement.ConfirmedAt = nil
		if err := tx.Save(&settlement).Error; err != nil {
			return settlement, err
		}
		// 旧明细物理删除，全部按最新考勤重建
		if err := tx.Unscoped().Where("settlement_id = ?", settlement.ID).Delete(&models.SettlementItem{}).Error; err != nil {
			return settlement, err
		}
	}

	for i := range items {
		items[i].SettlementID = settlement.ID
	}
	if len(items) > 0 {
		if err := tx.Create(&items).Error; err != nil {
			return settlement, err
		}
	}

	return settlement, nil
}

// markSettlementStale 考勤变动后，把对应教师月份已确认的单子标成待重算
func markSettlementStale(tx *gorm.DB, teacherID uint, month string) error {
	if teacherID == 0 || month == "" {
		return nil
	}
	return tx.Model(&models.TeacherSettlement{}).
		Where("teacher_id = ? AND month = ? AND status = ?", teacherID, month, SettlementStatusConfirmed).
		Update("status", SettlementStatusStale).Error
}

// GenerateSettlements 按月生成结算单：只统计当月已点名的课；
// 已确认/待重算的单子保持锁定跳过，待确认的单子按最新考勤重算。
func GenerateSettlements(c *gin.Context) {
	var req struct {
		Month string `json:"month" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !isValidMonth(req.Month) {
		utils.BadRequest(c, "请选择正确的月份（格式 YYYY-MM）")
		return
	}
	month := req.Month

	var teachers []models.Teacher
	if err := database.DB.Find(&teachers).Error; err != nil {
		utils.InternalServerError(c, "查询教师失败")
		return
	}

	generated := make([]map[string]interface{}, 0)
	skipped := make([]map[string]interface{}, 0)

	txErr := database.DB.Transaction(func(tx *gorm.DB) error {
		for _, teacher := range teachers {
			lessons, err := listAttendedLessons(teacher.ID, month)
			if err != nil {
				return err
			}

			var settlement models.TeacherSettlement
			findErr := tx.Where("teacher_id = ? AND month = ?", teacher.ID, month).First(&settlement).Error
			if findErr != nil && findErr != gorm.ErrRecordNotFound {
				return findErr
			}

			// 已确认/待重算的单子金额锁住，批量生成不覆盖；
			// 待重算的单子需显式"重开重算"后才会刷新
			if findErr == nil &&
				(settlement.Status == SettlementStatusConfirmed || settlement.Status == SettlementStatusStale) {
				skipped = append(skipped, map[string]interface{}{
					"teacher_id":   teacher.ID,
					"teacher_name": teacher.Name,
					"status":       settlement.Status,
				})
				continue
			}

			// 当月没有已点名的课：已有未锁定单子也刷新为 0，没有则不建空单
			if findErr == gorm.ErrRecordNotFound && len(lessons) == 0 {
				continue
			}

			s, err := rebuildSettlement(tx, teacher, month)
			if err != nil {
				return err
			}
			generated = append(generated, map[string]interface{}{
				"settlement_id": s.ID,
				"teacher_id":    teacher.ID,
				"teacher_name":  teacher.Name,
				"total_hours":   s.TotalHours,
				"total_amount":  s.TotalAmount,
				"lesson_count":  len(lessons),
			})
		}
		return nil
	})
	if txErr != nil {
		utils.InternalServerError(c, "生成结算单失败")
		return
	}

	utils.SuccessWithMessage(c, "结算单生成完成", gin.H{
		"month":     month,
		"generated": generated,
		"skipped":   skipped,
	})
}

func GetSettlements(c *gin.Context) {
	month := c.Query("month")
	teacherID := c.Query("teacher_id")
	status := c.Query("status")

	query := database.DB.Model(&models.TeacherSettlement{}).Preload("Teacher")
	if month != "" {
		query = query.Where("month = ?", month)
	}
	if teacherID != "" {
		query = query.Where("teacher_id = ?", teacherID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var settlements []models.TeacherSettlement
	if err := query.Order("month DESC, status ASC, teacher_id ASC").Find(&settlements).Error; err != nil {
		utils.InternalServerError(c, "查询失败")
		return
	}

	utils.Success(c, settlements)
}

func GetSettlement(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	var settlement models.TeacherSettlement
	if err := database.DB.Preload("Teacher").Preload("Items").First(&settlement, id).Error; err != nil {
		utils.NotFound(c, "结算单不存在")
		return
	}

	utils.Success(c, settlement)
}

// ConfirmSettlement 财务确认：锁定金额
func ConfirmSettlement(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	var settlement models.TeacherSettlement
	if err := database.DB.First(&settlement, id).Error; err != nil {
		utils.NotFound(c, "结算单不存在")
		return
	}

	if settlement.Status == SettlementStatusConfirmed {
		utils.BadRequest(c, "该结算单已确认，无需重复确认")
		return
	}
	if settlement.Status == SettlementStatusStale {
		utils.BadRequest(c, "考勤有补录，请先重开重算再确认")
		return
	}

	now := time.Now()
	settlement.Status = SettlementStatusConfirmed
	settlement.ConfirmedAt = &now
	if err := database.DB.Save(&settlement).Error; err != nil {
		utils.InternalServerError(c, "确认失败")
		return
	}

	utils.SuccessWithMessage(c, "已确认，金额已锁定", settlement)
}

// ReopenSettlement 重开结算单：解锁并立即按最新考勤重算。
// 适用于"已确认"（发现有误）和"待重算"（确认后有补录）两种情况。
func ReopenSettlement(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	var settlement models.TeacherSettlement
	if err := database.DB.First(&settlement, id).Error; err != nil {
		utils.NotFound(c, "结算单不存在")
		return
	}

	if settlement.Status != SettlementStatusConfirmed && settlement.Status != SettlementStatusStale {
		utils.BadRequest(c, "只有已确认或待重算的单子需要重开")
		return
	}

	var teacher models.Teacher
	if err := database.DB.First(&teacher, settlement.TeacherID).Error; err != nil {
		utils.NotFound(c, "教师不存在")
		return
	}

	var rebuilt models.TeacherSettlement
	txErr := database.DB.Transaction(func(tx *gorm.DB) error {
		s, err := rebuildSettlement(tx, teacher, settlement.Month)
		if err != nil {
			return err
		}
		rebuilt = s
		return nil
	})
	if txErr != nil {
		utils.InternalServerError(c, "重开重算失败")
		return
	}

	if err := database.DB.Preload("Teacher").Preload("Items").First(&rebuilt, rebuilt.ID).Error; err != nil {
		utils.InternalServerError(c, "查询重算结果失败")
		return
	}

	utils.SuccessWithMessage(c, "已按最新考勤重开重算", rebuilt)
}

func isValidMonth(month string) bool {
	if len(month) != 7 || month[4] != '-' {
		return false
	}
	_, err := time.Parse("2006-01", month)
	return err == nil
}
