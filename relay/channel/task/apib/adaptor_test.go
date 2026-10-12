package apib

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBillingModelUsesResolutionTier(t *testing.T) {
	assert.Equal(t, "aux/seedance-2.0-mini", billingModel("seedance-2.0-mini", "720p"))
	assert.Equal(t, "aux/seedance-2.0-mini-480p", billingModel("seedance-2.0-mini", "480p"))
	assert.Equal(t, "aux/MiniMax-H3-768p", billingModel("MiniMax-H3", "768P"))
	assert.Equal(t, "aux/MiniMax-H3", billingModel("MiniMax-H3", "2K"))
	assert.Equal(t, "aux/kling-3.0-turbo", billingModel("kling-3.0-turbo", "720p"))
	assert.Equal(t, "aux/kling-v3-motion-control", billingModel("kling-v3-motion-control", "720p"))
	assert.Equal(t, "aux/kling-v3-720p", billingModel("kling-v3", "720p"))
}

func TestTaskSubmissionUsesPublicIDAndKeepsUpstreamIDPrivate(t *testing.T) {
	adaptor := &TaskAdaptor{}
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	info := &common.RelayInfo{TaskRelayInfo: &common.TaskRelayInfo{}}
	info.PublicTaskID = "task_public"
	response := &http.Response{Body: io.NopCloser(strings.NewReader(`{"code":200,"data":[{"task_id":"upstream_secret","status":"submitted"}]}`))}

	upstreamID, raw, taskError := adaptor.DoResponse(context, response, info)
	require.Nil(t, taskError)
	assert.Equal(t, "upstream_secret", upstreamID)
	assert.Contains(t, string(raw), "upstream_secret")
	assert.JSONEq(t, `{"task_id":"task_public","status":"submitted"}`, recorder.Body.String())
}

func TestTaskStatusProvidesOutputForDeliveryAndRefundState(t *testing.T) {
	adaptor := &TaskAdaptor{}
	success, err := adaptor.ParseTaskResult([]byte(`{"data":{"status":"completed","result":{"videos":[{"url":["https://cdn.example/video.mp4"]}]}}}`))
	require.NoError(t, err)
	assert.Equal(t, "SUCCESS", success.Status)
	assert.Equal(t, "https://cdn.example/video.mp4", success.Url)

	failure, err := adaptor.ParseTaskResult([]byte(`{"data":{"status":"failed","error":{"message":"rejected"}}}`))
	require.NoError(t, err)
	assert.Equal(t, "FAILURE", failure.Status)
	assert.Equal(t, "rejected", failure.Reason)
}
