package studyflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"studyflow/internal/identity"

	"github.com/gin-gonic/gin"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The MCP tools are a second entry point onto the same *Service the REST API
// uses. Business rules therefore cannot drift between the two: both go through
// the same validation, the same transaction and the same optimistic locking.
//
// The argument and result types deliberately reuse the HTTP DTOs
// (ImportMilestoneInput, ImportTaskInput) so the contract has one definition.

// ImportPlanTreeArgs is the MCP form of a batch import. It is ImportPlanTreeInput
// plus the idempotency key, which the caller generates and reuses when retrying.
type ImportPlanTreeArgs struct {
	GoalID                string                 `json:"goal_id" jsonschema:"计划挂在哪个目标下，目标 UUID"`
	IdempotencyKey        string                 `json:"idempotency_key" jsonschema:"调用方生成的幂等键，8 到 200 个可打印 ASCII 字符。超时后用同一个键重试不会重复建计划；换一个键才会新建"`
	Title                 string                 `json:"title" jsonschema:"计划名称"`
	Description           string                 `json:"description,omitempty" jsonschema:"计划说明"`
	Mode                  string                 `json:"mode" jsonschema:"calendar 按日期安排到天；sequence 按课时依次推进。sequence 模式下任务不能带日期"`
	WeeklyCapacityMinutes uint                   `json:"weekly_capacity_minutes,omitempty" jsonschema:"每周计划投入的分钟数，用于复盘里的计划投入对比"`
	StartDate             *string                `json:"start_date,omitempty" jsonschema:"计划开始日期，格式 YYYY-MM-DD"`
	EndDate               *string                `json:"end_date,omitempty" jsonschema:"计划结束日期，格式 YYYY-MM-DD"`
	Milestones            []ImportMilestoneInput `json:"milestones,omitempty" jsonschema:"阶段列表，任务嵌在所属阶段里，顺序即阶段顺序"`
	Tasks                 []ImportTaskInput      `json:"tasks,omitempty" jsonschema:"不归属任何阶段的任务"`
}

type ImportPlanTreeOutput struct {
	ImportID      string `json:"import_id" jsonschema:"本次导入的回执 ID，重放时返回第一次的 ID"`
	Replayed      bool   `json:"replayed" jsonschema:"true 表示这次是重放，没有新建任何数据"`
	PlanID        string `json:"plan_id" jsonschema:"计划 UUID"`
	PlanVersionID string `json:"plan_version_id" jsonschema:"V1 草稿的 UUID"`
	Milestones    int    `json:"milestones" jsonschema:"本次建出的阶段数"`
	Tasks         int    `json:"tasks" jsonschema:"本次建出的任务数"`
	VersionStatus string `json:"version_status" jsonschema:"导入后版本的状态，正常是 draft，需要用户在 StudyFlow 里激活才会进入执行"`
}

type QueryProgressArgs struct {
	PlanID string `json:"plan_id,omitempty" jsonschema:"只看某个计划；留空则返回全部生效中的计划"`
	From   string `json:"from,omitempty" jsonschema:"起始日期 YYYY-MM-DD，按任务的计划日期筛选"`
	To     string `json:"to,omitempty" jsonschema:"结束日期 YYYY-MM-DD"`
	Status string `json:"status,omitempty" jsonschema:"按任务状态筛选：todo、in_progress、done、canceled"`
}

type QueryProgressOutput struct {
	Plans []PlanProgress `json:"plans" jsonschema:"生效中的计划及其完成进度"`
	Tasks []TaskProgress `json:"tasks" jsonschema:"符合条件的任务，带乐观锁版本号，改期时要用"`
}

type PlanProgress struct {
	PlanID        string `json:"plan_id"`
	Title         string `json:"title"`
	Mode          string `json:"mode"`
	DoneTasks     int    `json:"done_tasks"`
	TotalTasks    int    `json:"total_tasks"`
	ActiveVersion string `json:"active_version,omitempty" jsonschema:"生效版本号；为空说明还没有激活任何版本"`
}

type TaskProgress struct {
	TaskID        string `json:"task_id"`
	PlanTitle     string `json:"plan_title"`
	Title         string `json:"title"`
	ScheduledDate string `json:"scheduled_date,omitempty"`
	Status        string `json:"status"`
	Version       uint   `json:"version" jsonschema:"任务当前版本号，改期时必须原样回传"`
}

type RescheduleTaskArgs struct {
	TaskID             string  `json:"task_id" jsonschema:"任务 UUID"`
	Version            uint    `json:"version" jsonschema:"任务当前版本号，从 query_progress 得到。版本过期会被拒绝，这是为了避免并发改期静默覆盖"`
	ScheduledDate      *string `json:"scheduled_date,omitempty" jsonschema:"新的计划日期，格式 YYYY-MM-DD"`
	ClearScheduledDate bool    `json:"clear_scheduled_date,omitempty" jsonschema:"设为 true 表示取消日期安排，改回未安排状态"`
}

type RescheduleTaskOutput struct {
	TaskID        string `json:"task_id"`
	Title         string `json:"title"`
	ScheduledDate string `json:"scheduled_date,omitempty"`
	Status        string `json:"status"`
	Version       uint   `json:"version" jsonschema:"改期后的新版本号，下次改期要用这个"`
}

// MCPHandler adapts the application service to MCP tools.
type MCPHandler struct{ service *Service }

// RegisterMCP mounts the MCP endpoint on the same router as the REST API, so one
// process owns the database connection and the goose migrations.
func RegisterMCP(router *gin.Engine, service *Service, authenticator *identity.Authenticator, logger *slog.Logger) {
	handler := &MCPHandler{service: service}
	server := mcp.NewServer(
		&mcp.Implementation{Name: "studyflow", Version: "v1.0.0"},
		&mcp.ServerOptions{
			Logger:       logger,
			Instructions: "StudyFlow 是任务执行端：它保存已经确定好内容和日期的任务，不负责拆解目标或决定任务安排在哪一天。导入的计划会落在草稿版本上，需要用户确认激活后才进入执行。",
		},
	)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "import_plan_tree",
		Description: "把一条已经确定好的学习路线一次事务导入成计划树：计划 + V1 草稿 + 阶段 + 任务。必须带上调用方生成的幂等键；超时重试请复用同一个键，重复请求会返回第一次的结果而不是再建一份。任何校验失败都会整批回滚。",
	}, handler.importPlanTree)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_progress",
		Description: "查询计划进度和任务列表。返回每个生效计划的完成数/总数，以及符合筛选条件的任务。任务里带着版本号，改期时要用。",
	}, handler.queryProgress)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "reschedule_task",
		Description: "修改某个任务的计划日期，或取消它的日期安排。必须带上从 query_progress 拿到的版本号；版本过期会被拒绝而不是静默覆盖。",
	}, handler.rescheduleTask)

	// Stateless: every request carries its own bearer token, and these tools never
	// call back into the client, so there is no session to hijack.
	streamable := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, Logger: logger},
	)

	// The verifier is the same RS256/JWKS/issuer/audience check the REST API uses,
	// so a token minted for another audience still cannot call these tools.
	verifier := func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		principal, err := authenticator.Parse(ctx, token)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", auth.ErrInvalidToken, err)
		}
		// The SDK middleware insists on an expiry, so pass along the one the token
		// declared rather than inventing a fresh window.
		return &auth.TokenInfo{UserID: principal.UserID, Expiration: principal.ExpiresAt}, nil
	}
	router.Any("/mcp", gin.WrapH(auth.RequireBearerToken(verifier, nil)(streamable)))
}

func (h *MCPHandler) importPlanTree(ctx context.Context, req *mcp.CallToolRequest, args ImportPlanTreeArgs) (*mcp.CallToolResult, ImportPlanTreeOutput, error) {
	userID, err := mcpUserID(req)
	if err != nil {
		return nil, ImportPlanTreeOutput{}, err
	}
	result, err := h.service.ImportPlanTree(ctx, userID, args.IdempotencyKey, ImportPlanTreeInput{
		GoalID:                strings.TrimSpace(args.GoalID),
		Title:                 args.Title,
		Description:           args.Description,
		Mode:                  strings.TrimSpace(args.Mode),
		WeeklyCapacityMinutes: args.WeeklyCapacityMinutes,
		StartDate:             args.StartDate,
		EndDate:               args.EndDate,
		Milestones:            args.Milestones,
		Tasks:                 args.Tasks,
	})
	if err != nil {
		return nil, ImportPlanTreeOutput{}, toolError("导入计划失败", err)
	}
	version := importedVersion(result.Plan, result.PlanVersionID)
	output := ImportPlanTreeOutput{
		ImportID:      result.ImportID,
		Replayed:      result.Replayed,
		PlanID:        result.PlanID,
		PlanVersionID: result.PlanVersionID,
		VersionStatus: version.Status,
	}
	for _, milestone := range version.Milestones {
		output.Milestones++
		output.Tasks += len(milestone.Tasks)
	}
	output.Tasks += len(version.UnassignedTasks)
	return nil, output, nil
}

// importedVersion finds the version the import created. GetPlan orders versions
// newest first, so Versions[0] is not necessarily it: once the user clones a new
// draft, a replay would otherwise describe that newer version instead.
func importedVersion(detail *PlanDetail, versionID string) VersionView {
	for _, version := range detail.Versions {
		if version.ID == versionID {
			return version
		}
	}
	return VersionView{}
}

func (h *MCPHandler) queryProgress(ctx context.Context, req *mcp.CallToolRequest, args QueryProgressArgs) (*mcp.CallToolResult, QueryProgressOutput, error) {
	userID, err := mcpUserID(req)
	if err != nil {
		return nil, QueryProgressOutput{}, err
	}
	filter := TaskFilter{PlanID: strings.TrimSpace(args.PlanID), Status: strings.TrimSpace(args.Status)}
	if value := strings.TrimSpace(args.From); value != "" {
		date, err := parseDate(value)
		if err != nil {
			return nil, QueryProgressOutput{}, fmt.Errorf("from 无效，需要 YYYY-MM-DD 格式的日期")
		}
		filter.From = date
	}
	if value := strings.TrimSpace(args.To); value != "" {
		date, err := parseDate(value)
		if err != nil {
			return nil, QueryProgressOutput{}, fmt.Errorf("to 无效，需要 YYYY-MM-DD 格式的日期")
		}
		filter.To = date
	}

	tasks, err := h.service.ListTasks(ctx, userID, filter)
	if err != nil {
		return nil, QueryProgressOutput{}, toolError("查询任务失败", err)
	}
	tree, err := h.service.GoalTree(ctx, userID)
	if err != nil {
		return nil, QueryProgressOutput{}, toolError("查询计划失败", err)
	}

	output := QueryProgressOutput{Plans: []PlanProgress{}, Tasks: []TaskProgress{}}
	only := strings.TrimSpace(args.PlanID)
	for _, goal := range tree {
		collectPlanProgress(goal, only, &output.Plans)
	}
	for _, task := range tasks {
		output.Tasks = append(output.Tasks, TaskProgress{
			TaskID:        task.ID,
			PlanTitle:     task.PlanTitle,
			Title:         task.Title,
			ScheduledDate: stringValue(task.ScheduledDate),
			Status:        task.Status,
			Version:       task.Version,
		})
	}
	return nil, output, nil
}

func (h *MCPHandler) rescheduleTask(ctx context.Context, req *mcp.CallToolRequest, args RescheduleTaskArgs) (*mcp.CallToolResult, RescheduleTaskOutput, error) {
	userID, err := mcpUserID(req)
	if err != nil {
		return nil, RescheduleTaskOutput{}, err
	}
	if args.Version == 0 {
		return nil, RescheduleTaskOutput{}, errors.New("version 必填：先用 query_progress 拿到任务当前的版本号")
	}
	input := UpdateTaskInput{Version: args.Version, ClearScheduledDate: args.ClearScheduledDate}
	if args.ScheduledDate != nil {
		if value := strings.TrimSpace(*args.ScheduledDate); value != "" {
			input.ScheduledDate = &value
		}
	}
	if input.ScheduledDate == nil && !input.ClearScheduledDate {
		return nil, RescheduleTaskOutput{}, errors.New("必须给出 scheduled_date，或者把 clear_scheduled_date 设为 true")
	}
	task, err := h.service.UpdateTask(ctx, userID, strings.TrimSpace(args.TaskID), input)
	if err != nil {
		return nil, RescheduleTaskOutput{}, toolError("改期失败", err)
	}
	return nil, RescheduleTaskOutput{
		TaskID:        task.ID,
		Title:         task.Title,
		ScheduledDate: stringValue(task.ScheduledDate),
		Status:        task.Status,
		Version:       task.Version,
	}, nil
}

// collectPlanProgress walks the goal tree and appends each plan's progress. When
// only is set, plans of other goals are skipped.
func collectPlanProgress(goal *GoalNode, only string, into *[]PlanProgress) {
	for _, plan := range goal.Plans {
		if only != "" && plan.ID != only {
			continue
		}
		entry := PlanProgress{PlanID: plan.ID, Title: plan.Title, Mode: plan.Mode, DoneTasks: plan.DoneTasks, TotalTasks: plan.TotalTasks}
		if plan.ActiveVersionNo != nil {
			entry.ActiveVersion = fmt.Sprintf("V%d", *plan.ActiveVersionNo)
		}
		*into = append(*into, entry)
	}
	for _, child := range goal.Children {
		collectPlanProgress(child, only, into)
	}
}

// mcpUserID reads the identity the bearer-token verifier attached to the call.
func mcpUserID(req *mcp.CallToolRequest) (string, error) {
	if req == nil {
		return "", errors.New("缺少请求上下文")
	}
	extra := req.GetExtra()
	if extra == nil || extra.TokenInfo == nil || extra.TokenInfo.UserID == "" {
		return "", errors.New("请求没有通过身份校验")
	}
	return extra.TokenInfo.UserID, nil
}

// toolError turns a domain error into something an agent can act on. Returning an
// error from a ToolHandlerFor marks the result as a tool error and puts the text
// in the content, so the model sees it instead of the call failing outright.
func toolError(action string, err error) error {
	switch {
	case errors.Is(err, ErrValidation):
		return fmt.Errorf("%s：参数无效", action)
	case errors.Is(err, ErrNotFound):
		return fmt.Errorf("%s：目标或任务不存在", action)
	case errors.Is(err, ErrIdempotencyConflict):
		return fmt.Errorf("%s：这个幂等键已经用于另一份不同的内容。要么改用同一份内容重试，要么换一个新的键", action)
	case errors.Is(err, ErrVersionConflict):
		return fmt.Errorf("%s：任务版本已过期，可能已被其它操作修改，请重新查询后再试", action)
	case errors.Is(err, ErrPlanVersionConflict), errors.Is(err, ErrConflict), errors.Is(err, ErrInvalidState):
		return fmt.Errorf("%s：当前状态不允许这个操作", action)
	default:
		return fmt.Errorf("%s：服务暂时不可用", action)
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
