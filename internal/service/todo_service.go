package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go-todo/internal/cache"
	"go-todo/internal/model"
	"go-todo/internal/repository"

	"gorm.io/gorm"
)

var ErrTodoNotFound = errors.New("todo not found")

// service依赖repository
type TodoService struct {
	repo         *repository.TodoRepository
	cache        *cache.JSONCache
	listCacheTTL time.Duration
	logger       *slog.Logger
}

// NewTodoService 创建一个新的 TodoService 实例，包含 TodoRepository
func NewTodoService(
	repo *repository.TodoRepository,
	jsonCache *cache.JSONCache,
	listCacheTTL time.Duration,
	logger *slog.Logger,
) *TodoService {
	return &TodoService{
		repo:         repo,
		cache:        jsonCache,
		listCacheTTL: listCacheTTL,
		logger:       logger,
	}
}

// 定义输入和输出结构体
type CreateTodoInput struct {
	Title       string
	Description string
}

type UpdateTodoInput struct {
	Title       string
	Description string
}

type ListTodosInput struct {
	Page      int
	PageSize  int
	Completed string
}

type ListTodosResult struct {
	Items      []model.Todo
	Page       int
	PageSize   int
	Total      int64
	TotalPages int
}

// 创建任务
func (s *TodoService) CreateTodo(
	ctx context.Context,
	userID uint64,
	input CreateTodoInput,
) (*model.Todo, error) {
	todo := model.Todo{
		UserID:      userID,
		Title:       input.Title,
		Description: input.Description,
	}

	if err := s.repo.Create(&todo); err != nil {
		return nil, err
	}

	s.invalidateTodoListCache(ctx, userID)

	return &todo, nil
}

// 根据ID获取任务
func (s *TodoService) GetTodoByID(
	userID uint64,
	id int64,
) (*model.Todo, error) {
	todo, err := s.repo.GetByID(userID, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTodoNotFound
		}

		return nil, err
	}

	return todo, nil
}

// 根据条件查询任务列表
func (s *TodoService) ListTodos(
	ctx context.Context,
	userID uint64,
	input ListTodosInput,
) (*ListTodosResult, error) {

	// 构造key
	cacheKey := buildTodoListCacheKey(userID, input)

	var cachedResult ListTodosResult

	//查询redis
	found, err := s.cache.Get(
		ctx,
		cacheKey,
		&cachedResult,
	)
	//三种情况都用logger处理
	if err != nil {
		s.logger.Warn(
			"读取 Todo 列表缓存失败，降级查询 MySQL",
			"cache_key", cacheKey,
			"error", err,
		)
	} else if found {
		s.logger.Info(
			"Todo 列表缓存命中",
			"cache_key", cacheKey,
		)

		return &cachedResult, nil
	} else {
		s.logger.Info(
			"Todo 列表缓存未命中",
			"cache_key", cacheKey,
		)
	}

	var completed *bool
	//处理完成条件
	if input.Completed != "" {
		value := input.Completed == "true"
		completed = &value
	}
	//计算页数
	offset := (input.Page - 1) * input.PageSize

	//包装查询条件
	params := repository.ListTodosParams{
		UserID:    userID,
		Completed: completed,
		Limit:     input.PageSize,
		Offset:    offset,
	}

	s.logger.Info(
		"回源 MySQL 查询 Todo 列表",
		"cache_key", cacheKey,
		"user_id", userID,
		"page", input.Page,
		"page_size", input.PageSize,
		"completed", input.Completed,
	)

	//查询总数
	total, err := s.repo.Count(params)
	if err != nil {
		return nil, err
	}

	//查询页数
	todos, err := s.repo.List(params)
	if err != nil {
		return nil, err
	}

	//总页数计算
	totalPages := int(
		(total + int64(input.PageSize) - 1) /
			int64(input.PageSize),
	)

	//查询结果
	result := &ListTodosResult{
		Items:      todos,
		Page:       input.Page,
		PageSize:   input.PageSize,
		Total:      total,
		TotalPages: totalPages,
	}

	//设置缓存
	if err := s.cache.Set(
		ctx,
		cacheKey,
		result,
		s.listCacheTTL,
	); err != nil {
		s.logger.Warn(
			"写入 Todo 列表缓存失败",
			"cache_key", cacheKey,
			"error", err,
		)
	} else {
		s.logger.Info(
			"写入 Todo 列表缓存成功",
			"cache_key", cacheKey,
			"ttl", s.listCacheTTL.String(),
		)
	}

	//返回结果
	return result, nil

}

// 更新任务
func (s *TodoService) UpdateTodo(
	ctx context.Context,
	userID uint64,
	id int64,
	input UpdateTodoInput,
) (*model.Todo, error) {
	if _, err := s.GetTodoByID(userID, id); err != nil {
		return nil, err
	}

	if err := s.repo.Update(
		userID,
		id,
		input.Title,
		input.Description,
	); err != nil {
		return nil, err
	}

	s.invalidateTodoListCache(ctx, userID)

	return s.GetTodoByID(userID, id)
}

// 标记完成
func (s *TodoService) CompleteTodo(
	ctx context.Context,
	userID uint64,
	id int64,
) (*model.Todo, error) {
	if _, err := s.GetTodoByID(userID, id); err != nil {
		return nil, err
	}

	if err := s.repo.MarkCompleted(userID, id); err != nil {
		return nil, err
	}

	s.invalidateTodoListCache(ctx, userID)

	return s.GetTodoByID(userID, id)
}

// 删除任务
func (s *TodoService) DeleteTodo(
	ctx context.Context,
	userID uint64,
	id int64,
) error {
	if _, err := s.GetTodoByID(userID, id); err != nil {
		return err
	}

	s.invalidateTodoListCache(ctx, userID)

	return s.repo.Delete(userID, id)
}

// 拼接redis的key
func buildTodoListCacheKey(
	userID uint64,
	input ListTodosInput,
) string {
	completed := input.Completed
	if completed == "" {
		completed = "all"
	}

	return fmt.Sprintf(
		"todo:list:user:%d:page:%d:size:%d:completed:%s",
		userID,
		input.Page,
		input.PageSize,
		completed,
	)
}

// 拼接用户缓存匹配模式
func buildTodoListCachePattern(userID uint64) string {
	return fmt.Sprintf(
		"todo:list:user:%d:*",
		userID,
	)
}

// 删除指定用户的所有 Todo 列表缓存。
func (s *TodoService) invalidateTodoListCache(
	ctx context.Context,
	userID uint64,
) {
	pattern := buildTodoListCachePattern(userID)

	deleted, err := s.cache.DeleteByPattern(ctx, pattern)
	if err != nil {
		s.logger.Warn(
			"删除 Todo 列表缓存失败",
			"cache_pattern", pattern,
			"user_id", userID,
			"error", err,
		)
		return
	}

	s.logger.Info(
		"Todo 列表缓存已失效",
		"cache_pattern", pattern,
		"user_id", userID,
		"deleted_count", deleted,
	)
}
