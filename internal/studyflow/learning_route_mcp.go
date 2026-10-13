package studyflow

import (
	"context"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"slices"
)

type RouteReceiptArgs struct {
	IdempotencyKey string `json:"idempotency_key"`
}

func routeMCPIdentity(req *mcp.CallToolRequest, key, digest, scope string) (string, error) {
	if req == nil || req.GetExtra() == nil || req.GetExtra().TokenInfo == nil {
		return "", errors.New("缺少身份")
	}
	token := req.GetExtra().TokenInfo
	if delegated, _ := token.Extra["delegated"].(bool); delegated {
		if !slices.Contains(token.Scopes, scope) || token.Extra["import_key"] != key || (digest != "" && token.Extra["content_digest"] != digest) {
			return "", errors.New("令牌权限或导入摘要不匹配")
		}
	}
	if token.UserID == "" {
		return "", errors.New("缺少身份")
	}
	return token.UserID, nil
}
func (h *MCPHandler) importLearningRoute(ctx context.Context, req *mcp.CallToolRequest, args LearningRouteArgs) (*mcp.CallToolResult, LearningRouteReceipt, error) {
	digest, err := LearningRouteDigest(args.Payload)
	if err != nil {
		return nil, LearningRouteReceipt{}, err
	}
	user, err := routeMCPIdentity(req, args.IdempotencyKey, digest, "learning_route:import")
	if err != nil {
		return nil, LearningRouteReceipt{}, err
	}
	receipt, err := h.service.ImportLearningRoute(ctx, user, args.IdempotencyKey, args.Payload)
	if err != nil {
		return nil, LearningRouteReceipt{}, toolError("导入学习路线失败", err)
	}
	return nil, *receipt, nil
}
func (h *MCPHandler) getLearningRouteImport(ctx context.Context, req *mcp.CallToolRequest, args RouteReceiptArgs) (*mcp.CallToolResult, LearningRouteReceipt, error) {
	user, err := routeMCPIdentity(req, args.IdempotencyKey, "", "learning_route:read")
	if err != nil {
		return nil, LearningRouteReceipt{}, err
	}
	receipt, err := h.service.GetLearningRouteImport(ctx, user, args.IdempotencyKey)
	if err != nil {
		return nil, LearningRouteReceipt{}, toolError("查询导入回执失败", err)
	}
	if delegated, _ := req.GetExtra().TokenInfo.Extra["delegated"].(bool); delegated && req.GetExtra().TokenInfo.Extra["content_digest"] != receipt.ContentDigest {
		return nil, LearningRouteReceipt{}, errors.New("导入摘要不匹配")
	}
	return nil, *receipt, nil
}
