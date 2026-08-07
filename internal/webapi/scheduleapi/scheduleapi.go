package scheduleapi

import (
	cfgpkg "asa-server/internal/config"
	"asa-server/internal/schedule"
	"asa-server/internal/webapi/apiresp"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) RegisterRouter(r *gin.Engine) {
	s := r.Group("/api/schedule")
	{
		s.GET("/tasks", h.listTasks)
		s.POST("/tasks", h.createTask)
		s.PUT("/tasks/:id", h.updateTask)
		s.DELETE("/tasks/:id", h.deleteTask)
		s.POST("/tasks/:id/toggle", h.toggleTask)
		s.POST("/tasks/:id/run", h.runTaskNow)
		s.GET("/logs", h.listRunLogs)
		s.DELETE("/logs", h.clearRunLogs)
		s.GET("/runs", h.listRuns)
		s.POST("/runs/:run_id/cancel", h.cancelRun)
		s.GET("/pending-restore", h.getPendingRestore)
		s.POST("/pending-restore/confirm", h.confirmPendingRestore)
		s.DELETE("/pending-restore", h.ignorePendingRestore)
	}
}

// TaskRequest 是新建/修改任务的请求体。
// 不直接复用 schedule.Task：那样会让客户端能覆写 ID / LastRunAt / NextRunAt 这些
// 由服务端维护的字段。
type TaskRequest struct {
	Name      string            `json:"name"`
	Type      schedule.TaskType `json:"type"`
	Enabled   bool              `json:"enabled"`
	Rule      schedule.RuleType `json:"rule"`
	Every     int               `json:"every"`
	At        string            `json:"at"`
	Instances []string          `json:"instances"`

	Countdown     int    `json:"countdown"`
	NotifyPoints  []int  `json:"notify_points"`
	NotifyMessage string `json:"notify_message"`
	NotifyCommand string `json:"notify_command"`
}

func (r *TaskRequest) toTask() *schedule.Task {
	return &schedule.Task{
		Name:          r.Name,
		Type:          r.Type,
		Enabled:       r.Enabled,
		Rule:          r.Rule,
		Every:         r.Every,
		At:            r.At,
		Instances:     r.Instances,
		Countdown:     r.Countdown,
		NotifyPoints:  r.NotifyPoints,
		NotifyMessage: r.NotifyMessage,
		NotifyCommand: r.NotifyCommand,
	}
}

func badRequest(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, apiresp.StatusResponse{Success: false, Error: err.Error()})
}

// validateInstances 确认任务里指定的实例都真实存在。
// schedule.Task.Validate 是纯粹的规则校验，够不着实例目录，所以这一层放在 API。
func validateInstances(names []string) error {
	if len(names) == 0 {
		return nil
	}

	available, err := cfgpkg.GetAvailableInstances()
	if err != nil {
		return fmt.Errorf("获取实例列表失败: %w", err)
	}

	for _, name := range names {
		if !slices.Contains(available, name) {
			return fmt.Errorf("实例不存在: %s", name)
		}
	}
	return nil
}

func (h *Handler) listTasks(c *gin.Context) {
	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Data:    gin.H{"tasks": schedule.GetGlobalScheduler().ListTasks()},
	})
}

func (h *Handler) createTask(c *gin.Context) {
	var req TaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	if err := validateInstances(req.Instances); err != nil {
		badRequest(c, err)
		return
	}

	task := req.toTask()
	if err := schedule.GetGlobalScheduler().AddTask(task); err != nil {
		badRequest(c, err)
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "定时任务已创建",
		Data:    gin.H{"task": task},
	})
}

func (h *Handler) updateTask(c *gin.Context) {
	var req TaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}
	if err := validateInstances(req.Instances); err != nil {
		badRequest(c, err)
		return
	}

	task := req.toTask()
	task.ID = c.Param("id")

	if err := schedule.GetGlobalScheduler().UpdateTask(task); err != nil {
		badRequest(c, err)
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Message: "定时任务已更新",
		Data:    gin.H{"task": task},
	})
}

func (h *Handler) deleteTask(c *gin.Context) {
	if err := schedule.GetGlobalScheduler().DeleteTask(c.Param("id")); err != nil {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{Success: true, Message: "定时任务已删除"})
}

func (h *Handler) toggleTask(c *gin.Context) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, err)
		return
	}

	if err := schedule.GetGlobalScheduler().ToggleTask(c.Param("id"), req.Enabled); err != nil {
		badRequest(c, err)
		return
	}

	msg := "定时任务已停用"
	if req.Enabled {
		msg = "定时任务已启用"
	}
	c.JSON(http.StatusOK, apiresp.StatusResponse{Success: true, Message: msg})
}

// listRunLogs 查询执行日志。
//
//	?task_id=xxx  只看某个任务（可选）
//	?limit=100    返回条数，缺省 100，上限即存储上限
//
// total 是过滤后、截断前的总数，供前端显示「共 N 条」。
func (h *Handler) listRunLogs(c *gin.Context) {
	limit, err := strconv.Atoi(strings.TrimSpace(c.Query("limit")))
	if err != nil {
		// 不传或传了非数字都按缺省处理：这个参数是纯优化项，
		// 为它返回 400 只会让前端多一条无意义的错误分支
		limit = 0
	}

	logs, total := schedule.GetGlobalScheduler().ListRunLogs(c.Query("task_id"), limit)

	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Data:    gin.H{"logs": logs, "total": total},
	})
}

// clearRunLogs 清空全部执行日志。
func (h *Handler) clearRunLogs(c *gin.Context) {
	if err := schedule.GetGlobalScheduler().ClearRunLogs(); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("清空执行日志失败: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{Success: true, Message: "执行日志已清空"})
}

// runTaskNow 立即执行一次，不影响 NextRunAt。任务在后台跑，接口立即返回。
func (h *Handler) runTaskNow(c *gin.Context) {
	if err := schedule.GetGlobalScheduler().RunNow(c.Param("id")); err != nil {
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{Success: false, Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, apiresp.StatusResponse{Success: true, Message: "已触发执行"})
}

// listRuns 返回当前正在执行的任务，供前端渲染「取消」按钮与执行阶段标签。
func (h *Handler) listRuns(c *gin.Context) {
	c.JSON(http.StatusOK, apiresp.StatusResponse{
		Success: true,
		Data:    gin.H{"runs": schedule.GetGlobalScheduler().ListRuns()},
	})
}

// cancelRun 取消一次正在执行的任务；状态回滚（把执行前活着的实例拉回来）
// 由任务自己的 goroutine 完成，这里只负责传导取消信号并立即返回。
func (h *Handler) cancelRun(c *gin.Context) {
	err := schedule.GetGlobalScheduler().CancelRun(c.Param("run_id"))
	switch {
	case err == nil:
		c.JSON(http.StatusOK, apiresp.StatusResponse{Success: true, Message: "已取消，正在回滚实例状态"})
	case errors.Is(err, schedule.ErrRunNotFound):
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{Success: false, Error: err.Error()})
	case errors.Is(err, schedule.ErrRunAlreadyCancelling):
		c.JSON(http.StatusConflict, apiresp.StatusResponse{Success: false, Error: err.Error()})
	default:
		badRequest(c, err)
	}
}

// getPendingRestore 查询「定时更新后未恢复启动」的现场，无现场时 pending 为 null——
// 前端每次挂载都会调它，不该用 404，那会在控制台刷一片红。
func (h *Handler) getPendingRestore(c *gin.Context) {
	p, ok := schedule.GetGlobalScheduler().GetPendingRestore()
	if !ok {
		c.JSON(http.StatusOK, apiresp.StatusResponse{Success: true, Data: gin.H{"pending": nil}})
		return
	}
	c.JSON(http.StatusOK, apiresp.StatusResponse{Success: true, Data: gin.H{"pending": p}})
}

// confirmPendingRestore 确认恢复：后台批量启动，接口立即返回，进度走批量操作既有的 SSE/WS。
func (h *Handler) confirmPendingRestore(c *gin.Context) {
	err := schedule.GetGlobalScheduler().ConfirmPendingRestore()
	switch {
	case err == nil:
		c.JSON(http.StatusOK, apiresp.StatusResponse{Success: true, Message: "已开始恢复启动"})
	case errors.Is(err, schedule.ErrNoPendingRestore):
		c.JSON(http.StatusNotFound, apiresp.StatusResponse{Success: false, Error: err.Error()})
	case errors.Is(err, schedule.ErrRestoreInProgress):
		c.JSON(http.StatusConflict, apiresp.StatusResponse{Success: false, Error: err.Error()})
	default:
		badRequest(c, err)
	}
}

// ignorePendingRestore 忽略：删除现场记录，不再提示。幂等——无现场时同样返回成功，
// 两个页面同时点忽略不该报错。
func (h *Handler) ignorePendingRestore(c *gin.Context) {
	if err := schedule.GetGlobalScheduler().IgnorePendingRestore(); err != nil {
		c.JSON(http.StatusInternalServerError, apiresp.StatusResponse{
			Success: false,
			Error:   fmt.Sprintf("忽略待恢复现场失败: %v", err),
		})
		return
	}
	c.JSON(http.StatusOK, apiresp.StatusResponse{Success: true, Message: "已忽略，实例保持停止状态"})
}
