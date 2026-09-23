package studyflow

import (
	"errors"
	"net/http"
	"strings"

	"studyflow/internal/identity"
	"studyflow/internal/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type HTTPHandler struct{ service *Service }

func NewHTTPHandler(service *Service) *HTTPHandler { return &HTTPHandler{service: service} }

func RegisterRoutes(router *gin.Engine, handler *HTTPHandler, auth gin.HandlerFunc, db *gorm.DB) {
	router.GET("/health/live", func(c *gin.Context) { response.Success(c, gin.H{"status": "ok"}) })
	router.GET("/health/ready", func(c *gin.Context) {
		sqlDB, err := db.DB()
		if err != nil || sqlDB.PingContext(c.Request.Context()) != nil {
			c.JSON(http.StatusServiceUnavailable, response.APIResponse{Code: response.CodeInternalError, Message: "database unavailable", Data: nil})
			return
		}
		response.Success(c, gin.H{"status": "ready"})
	})
	api := router.Group("/api/v1")
	api.Use(auth)
	api.GET("/goals/tree", handler.goalTree)
	api.POST("/goals", handler.createGoal)
	api.PATCH("/goals/:id", handler.updateGoal)
	api.POST("/goals/:id/complete", handler.completeGoal)
	api.POST("/goals/:id/abandon", handler.abandonGoal)
	api.POST("/goals/:id/plans", handler.createPlan)
	api.GET("/plans", handler.listPlans)
	api.GET("/plans/:id", handler.getPlan)
	api.PATCH("/plans/:id", handler.updatePlan)
	api.POST("/plans/:id/versions", handler.clonePlanVersion)
	api.POST("/plan-versions/:id/activate", handler.activatePlanVersion)
	api.POST("/plan-versions/:id/milestones", handler.createMilestone)
	api.POST("/plan-versions/:id/tasks", handler.createTask)
	api.PATCH("/milestones/:id", handler.updateMilestone)
	api.DELETE("/milestones/:id", handler.deleteMilestone)
	api.GET("/tasks", handler.listTasks)
	api.PATCH("/tasks/:id", handler.updateTask)
	api.DELETE("/tasks/:id", handler.deleteTask)
	api.POST("/tasks/:id/complete", handler.completeTask)
	api.POST("/tasks/:id/reopen", handler.reopenTask)
	api.POST("/tasks/:id/cancel", handler.cancelTask)
	api.GET("/tasks/:id/sessions", handler.listTaskSessions)
	api.POST("/tasks/:id/sessions", handler.startSession)
	api.POST("/sessions/:id/finish", handler.finishSession)
	api.POST("/sessions/:id/discard", handler.discardSession)
	api.GET("/dashboard/today", handler.dashboard)
	api.GET("/reviews/weekly", handler.weeklyReview)
	api.GET("/preferences", handler.getPreferences)
	api.PATCH("/preferences", handler.updatePreferences)
}

func (h *HTTPHandler) createGoal(c *gin.Context) {
	var input CreateGoalInput
	if !bind(c, &input) {
		return
	}
	userID, _ := identity.UserID(c)
	value, err := h.service.CreateGoal(c, userID, input)
	write(c, value, err)
}
func (h *HTTPHandler) updateGoal(c *gin.Context) {
	var input UpdateGoalInput
	if !bind(c, &input) {
		return
	}
	userID, _ := identity.UserID(c)
	value, err := h.service.UpdateGoal(c, userID, c.Param("id"), input)
	write(c, value, err)
}
func (h *HTTPHandler) goalTree(c *gin.Context) {
	userID, _ := identity.UserID(c)
	value, err := h.service.GoalTree(c, userID)
	write(c, value, err)
}
func (h *HTTPHandler) completeGoal(c *gin.Context) { h.setGoalStatus(c, "achieved") }
func (h *HTTPHandler) abandonGoal(c *gin.Context)  { h.setGoalStatus(c, "abandoned") }
func (h *HTTPHandler) setGoalStatus(c *gin.Context, status string) {
	userID, _ := identity.UserID(c)
	err := h.service.SetGoalStatus(c, userID, c.Param("id"), status)
	write(c, gin.H{"status": status}, err)
}

func (h *HTTPHandler) createPlan(c *gin.Context) {
	var input CreatePlanInput
	if !bind(c, &input) {
		return
	}
	userID, _ := identity.UserID(c)
	value, err := h.service.CreatePlan(c, userID, c.Param("id"), input)
	write(c, value, err)
}
func (h *HTTPHandler) listPlans(c *gin.Context) {
	userID, _ := identity.UserID(c)
	value, err := h.service.ListPlans(c, userID, c.Query("goal_id"), c.Query("mode"))
	write(c, value, err)
}
func (h *HTTPHandler) getPlan(c *gin.Context) {
	userID, _ := identity.UserID(c)
	value, err := h.service.GetPlan(c, userID, c.Param("id"))
	write(c, value, err)
}
func (h *HTTPHandler) updatePlan(c *gin.Context) {
	var input UpdatePlanInput
	if !bind(c, &input) {
		return
	}
	userID, _ := identity.UserID(c)
	value, err := h.service.UpdatePlan(c, userID, c.Param("id"), input)
	write(c, value, err)
}
func (h *HTTPHandler) clonePlanVersion(c *gin.Context) {
	userID, _ := identity.UserID(c)
	value, err := h.service.ClonePlanVersion(c, userID, c.Param("id"))
	write(c, value, err)
}
func (h *HTTPHandler) activatePlanVersion(c *gin.Context) {
	userID, _ := identity.UserID(c)
	value, err := h.service.ActivatePlanVersion(c, userID, c.Param("id"))
	write(c, value, err)
}

func (h *HTTPHandler) createMilestone(c *gin.Context) {
	var input CreateMilestoneInput
	if !bind(c, &input) {
		return
	}
	userID, _ := identity.UserID(c)
	value, err := h.service.CreateMilestone(c, userID, c.Param("id"), input)
	write(c, value, err)
}
func (h *HTTPHandler) updateMilestone(c *gin.Context) {
	var input UpdateMilestoneInput
	if !bind(c, &input) {
		return
	}
	userID, _ := identity.UserID(c)
	value, err := h.service.UpdateMilestone(c, userID, c.Param("id"), input)
	write(c, value, err)
}
func (h *HTTPHandler) deleteMilestone(c *gin.Context) {
	userID, _ := identity.UserID(c)
	err := h.service.DeleteMilestone(c, userID, c.Param("id"))
	write(c, nil, err)
}
func (h *HTTPHandler) createTask(c *gin.Context) {
	var input CreateTaskInput
	if !bind(c, &input) {
		return
	}
	userID, _ := identity.UserID(c)
	value, err := h.service.CreateTask(c, userID, c.Param("id"), input)
	write(c, value, err)
}
func (h *HTTPHandler) updateTask(c *gin.Context) {
	var input UpdateTaskInput
	if !bind(c, &input) {
		return
	}
	userID, _ := identity.UserID(c)
	value, err := h.service.UpdateTask(c, userID, c.Param("id"), input)
	write(c, value, err)
}
func (h *HTTPHandler) deleteTask(c *gin.Context) {
	userID, _ := identity.UserID(c)
	err := h.service.DeleteTask(c, userID, c.Param("id"))
	write(c, gin.H{"deleted": true}, err)
}
func (h *HTTPHandler) completeTask(c *gin.Context) { h.setTaskStatus(c, "complete") }
func (h *HTTPHandler) reopenTask(c *gin.Context)   { h.setTaskStatus(c, "reopen") }
func (h *HTTPHandler) cancelTask(c *gin.Context)   { h.setTaskStatus(c, "cancel") }
func (h *HTTPHandler) setTaskStatus(c *gin.Context, action string) {
	userID, _ := identity.UserID(c)
	value, err := h.service.SetTaskStatus(c, userID, c.Param("id"), action)
	write(c, value, err)
}
func (h *HTTPHandler) listTasks(c *gin.Context) {
	userID, _ := identity.UserID(c)
	filter := TaskFilter{GoalID: c.Query("goal_id"), PlanID: c.Query("plan_id"), Status: c.Query("status"), Query: strings.TrimSpace(c.Query("q"))}
	if value := c.Query("from"); value != "" {
		date, err := parseDate(value)
		if err != nil {
			write(c, nil, err)
			return
		}
		filter.From = date
	}
	if value := c.Query("to"); value != "" {
		date, err := parseDate(value)
		if err != nil {
			write(c, nil, err)
			return
		}
		filter.To = date
	}
	value, err := h.service.ListTasks(c, userID, filter)
	write(c, value, err)
}

func (h *HTTPHandler) startSession(c *gin.Context) {
	userID, _ := identity.UserID(c)
	value, err := h.service.StartSession(c, userID, c.Param("id"))
	write(c, value, err)
}
func (h *HTTPHandler) listTaskSessions(c *gin.Context) {
	userID, _ := identity.UserID(c)
	value, err := h.service.ListTaskSessions(c, userID, c.Param("id"))
	write(c, value, err)
}
func (h *HTTPHandler) finishSession(c *gin.Context) {
	var input FinishSessionInput
	if c.Request.ContentLength > 0 && !bind(c, &input) {
		return
	}
	userID, _ := identity.UserID(c)
	value, err := h.service.FinishSession(c, userID, c.Param("id"), input)
	write(c, value, err)
}
func (h *HTTPHandler) discardSession(c *gin.Context) {
	userID, _ := identity.UserID(c)
	value, err := h.service.DiscardSession(c, userID, c.Param("id"))
	write(c, value, err)
}
func (h *HTTPHandler) dashboard(c *gin.Context) {
	userID, _ := identity.UserID(c)
	value, err := h.service.TodayDashboard(c, userID)
	write(c, value, err)
}
func (h *HTTPHandler) weeklyReview(c *gin.Context) {
	userID, _ := identity.UserID(c)
	value, err := h.service.WeeklyReview(c, userID, c.Query("week_start"))
	write(c, value, err)
}
func (h *HTTPHandler) getPreferences(c *gin.Context) {
	userID, _ := identity.UserID(c)
	value, err := h.service.GetPreferences(c, userID)
	write(c, value, err)
}
func (h *HTTPHandler) updatePreferences(c *gin.Context) {
	var input UpdatePreferenceInput
	if !bind(c, &input) {
		return
	}
	userID, _ := identity.UserID(c)
	value, err := h.service.UpdatePreferences(c, userID, input)
	write(c, value, err)
}

func bind(c *gin.Context, target any) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		response.FailInvalidArgument(c, "请求参数无效")
		return false
	}
	return true
}

func write(c *gin.Context, value any, err error) {
	if err == nil {
		response.Success(c, value)
		return
	}
	switch {
	case errors.Is(err, ErrNotFound):
		response.FailNotFound(c, "资源不存在")
	case errors.Is(err, ErrValidation):
		response.FailInvalidArgument(c, "请求参数无效")
	case errors.Is(err, ErrVersionConflict):
		response.FailConflict(c, "version_conflict")
	case errors.Is(err, ErrPlanVersionConflict):
		response.FailConflict(c, "plan_version_conflict")
	case errors.Is(err, ErrConflict):
		response.FailConflict(c, "当前状态冲突")
	case errors.Is(err, ErrInvalidState):
		response.FailConflict(c, "当前状态不允许此操作")
	default:
		response.FailInternalError(c, "服务暂时不可用")
	}
}
