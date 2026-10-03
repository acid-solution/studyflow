package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 统一响应体
type APIResponse struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

const (
	CodeOK              = 0
	CodeInvalidArgument = 10001
	CodeNotFound        = 10002
	CodeUnauthorized    = 10003
	CodeConflict        = 10004
	CodeInternalError   = 20001
)

// 响应辅助函数
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, APIResponse{
		Code:    CodeOK,
		Message: "success",
		Data:    data,
	})
}

func fail(c *gin.Context, status int, code int, message string) {
	c.JSON(status, APIResponse{
		Code:    code,
		Message: message,
		Data:    nil,
	})
}

func FailInvalidArgument(c *gin.Context, message string) {
	fail(c, http.StatusBadRequest, CodeInvalidArgument, message)
}

func FailNotFound(c *gin.Context, message string) {
	fail(c, http.StatusNotFound, CodeNotFound, message)
}

func FailInternalError(c *gin.Context, message string) {
	fail(c, http.StatusInternalServerError, CodeInternalError, message)
}

func FailUnauthorized(c *gin.Context, message string) {
	fail(c, http.StatusUnauthorized, CodeUnauthorized, message)
}

func FailConflict(c *gin.Context, message string) {
	fail(c, http.StatusConflict, CodeConflict, message)
}

func FailConflictData(c *gin.Context, code, message string, data any) {
	c.JSON(http.StatusConflict, APIResponse{
		Code:    code,
		Message: message,
		Data:    data,
	})
}
