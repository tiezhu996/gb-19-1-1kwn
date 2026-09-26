package controllers

import (
	"strconv"

	"edu-train/database"
	"edu-train/models"
	"edu-train/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// scheduleHasAttendance 判断该课是否已点名
func scheduleHasAttendance(scheduleID uint) bool {
	var count int64
	database.DB.Model(&models.Attendance{}).Where("schedule_id = ?", scheduleID).Count(&count)
	return count > 0
}

func GetSchedules(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	date := c.Query("date")
	teacherID := c.Query("teacher_id")
	classroomID := c.Query("classroom_id")
	status := c.Query("status")

	offset := (page - 1) * pageSize

	query := database.DB.Model(&models.Schedule{}).Preload("Course").Preload("Teacher").Preload("Classroom")

	if date != "" {
		query = query.Where("date = ?", date)
	}

	if teacherID != "" {
		query = query.Where("teacher_id = ?", teacherID)
	}

	if classroomID != "" {
		query = query.Where("classroom_id = ?", classroomID)
	}

	if status != "" {
		query = query.Where("status = ?", status)
	}

	var total int64
	query.Count(&total)

	var schedules []models.Schedule
	if err := query.Order("date ASC, start_time ASC").Offset(offset).Limit(pageSize).Find(&schedules).Error; err != nil {
		utils.InternalServerError(c, "查询失败")
		return
	}

	utils.Success(c, gin.H{
		"list":  schedules,
		"total": total,
		"page":  page,
		"page_size": pageSize,
	})
}

func GetSchedule(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	var schedule models.Schedule
	if err := database.DB.Preload("Course").Preload("Teacher").Preload("Classroom").Preload("Attendances.Student").First(&schedule, id).Error; err != nil {
		utils.NotFound(c, "排课不存在")
		return
	}

	utils.Success(c, schedule)
}

func CreateSchedule(c *gin.Context) {
	var schedule models.Schedule
	if err := c.ShouldBindJSON(&schedule); err != nil {
		utils.BadRequest(c, "参数错误")
		return
	}

	if hasConflict(schedule.TeacherID, schedule.ClassroomID, schedule.Date, schedule.StartTime, schedule.EndTime, 0) {
		utils.BadRequest(c, "教师或教室时间冲突")
		return
	}

	schedule.Status = "scheduled"

	if err := database.DB.Create(&schedule).Error; err != nil {
		utils.InternalServerError(c, "创建失败")
		return
	}

	utils.Success(c, schedule)
}

func UpdateSchedule(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	var schedule models.Schedule
	if err := database.DB.First(&schedule, id).Error; err != nil {
		utils.NotFound(c, "排课不存在")
		return
	}

	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		utils.BadRequest(c, "参数错误")
		return
	}

	teacherID := schedule.TeacherID
	classroomID := schedule.ClassroomID
	date := schedule.Date
	startTime := schedule.StartTime
	endTime := schedule.EndTime

	if v, ok := updates["teacher_id"]; ok {
		teacherID = uint(v.(float64))
	}
	if v, ok := updates["classroom_id"]; ok {
		classroomID = uint(v.(float64))
	}
	if v, ok := updates["date"]; ok {
		date = v.(string)
	}
	if v, ok := updates["start_time"]; ok {
		startTime = v.(string)
	}
	if v, ok := updates["end_time"]; ok {
		endTime = v.(string)
	}

	if hasConflict(teacherID, classroomID, date, startTime, endTime, uint(id)) {
		utils.BadRequest(c, "教师或教室时间冲突")
		return
	}

	// 记录变更前的教师与月份，便于把已确认的结算单标记为待重算
	oldTeacherID := schedule.TeacherID
	oldMonth := ""
	if len(schedule.Date) >= 7 {
		oldMonth = schedule.Date[:7]
	}

	if err := database.DB.Model(&schedule).Updates(updates).Error; err != nil {
		utils.InternalServerError(c, "更新失败")
		return
	}

	// 已点名的课发生改动，涉及的教师月份结算单都需要重算
	if scheduleHasAttendance(uint(id)) {
		newTeacherID := oldTeacherID
		newMonth := oldMonth
		if v, ok := updates["teacher_id"]; ok {
			if f, ok := v.(float64); ok {
				newTeacherID = uint(f)
			}
		}
		if v, ok := updates["date"]; ok {
			if s, ok := v.(string); ok && len(s) >= 7 {
				newMonth = s[:7]
			}
		}
		_ = database.DB.Transaction(func(tx *gorm.DB) error {
			_ = markSettlementStale(tx, oldTeacherID, oldMonth)
			if newTeacherID != oldTeacherID || newMonth != oldMonth {
				_ = markSettlementStale(tx, newTeacherID, newMonth)
			}
			return nil
		})
	}

	utils.Success(c, schedule)
}

func DeleteSchedule(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	var schedule models.Schedule
	if err := database.DB.First(&schedule, id).Error; err != nil {
		utils.NotFound(c, "排课不存在")
		return
	}

	attended := scheduleHasAttendance(uint(id))
	teacherID := schedule.TeacherID
	month := ""
	if len(schedule.Date) >= 7 {
		month = schedule.Date[:7]
	}

	if err := database.DB.Delete(&models.Schedule{}, id).Error; err != nil {
		utils.InternalServerError(c, "删除失败")
		return
	}

	// 删除已点名的课，对应教师月份已确认的结算单需重算
	if attended && month != "" {
		_ = database.DB.Transaction(func(tx *gorm.DB) error {
			return markSettlementStale(tx, teacherID, month)
		})
	}

	utils.Success(c, nil)
}

func hasConflict(teacherID, classroomID uint, date, startTime, endTime string, excludeID uint) bool {
	var count int64

	query := database.DB.Model(&models.Schedule{}).Where("date = ? AND status != ?", date, "cancelled")
	if excludeID > 0 {
		query = query.Where("id != ?", excludeID)
	}

	database.DB.Raw(
		"SELECT COUNT(*) FROM schedules WHERE date = ? AND teacher_id = ? AND id != ? AND status != ? AND ((start_time <= ? AND end_time > ?) OR (start_time < ? AND end_time >= ?))",
		date, teacherID, excludeID, "cancelled", startTime, startTime, endTime, endTime,
	).Scan(&count)

	if count > 0 {
		return true
	}

	database.DB.Raw(
		"SELECT COUNT(*) FROM schedules WHERE date = ? AND classroom_id = ? AND id != ? AND status != ? AND ((start_time <= ? AND end_time > ?) OR (start_time < ? AND end_time >= ?))",
		date, classroomID, excludeID, "cancelled", startTime, startTime, endTime, endTime,
	).Scan(&count)

	return count > 0
}
