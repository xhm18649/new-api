package controller

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// ClawHorseInternalTaskStatus lets the workflow service resume a gateway-owned
// task after restart without persisting a user's API token. The service secret
// alone is insufficient: every read must also match the original user ID.
func ClawHorseInternalTaskStatus(c *gin.Context) {
	configured := strings.TrimSpace(os.Getenv("CLAWHORSE_TASK_STATUS_SECRET"))
	provided := strings.TrimSpace(c.GetHeader("X-ClawHorse-Task-Secret"))
	if len(configured) < 32 || len(provided) != len(configured) || subtle.ConstantTimeCompare([]byte(configured), []byte(provided)) != 1 {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
		return
	}
	userID, err := strconv.Atoi(c.GetHeader("X-ClawHorse-User-ID"))
	if err != nil || userID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user"})
		return
	}
	task, exists, err := model.GetByTaskId(userID, c.Param("task_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "task lookup failed"})
		return
	}
	if !exists || task == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"data": gin.H{
		"task_id":     task.TaskID,
		"status":      task.Status,
		"result_url":  task.GetResultURL(),
		"fail_reason": task.FailReason,
	}})
}
