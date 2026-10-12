package apib

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// TaskAdaptor relays APIMart-style image and video tasks through the normal
// task billing lifecycle. The request body is retained so first-frame images,
// motion references, and model-specific fields reach the upstream unchanged.
type TaskAdaptor struct {
	taskcommon.BaseBilling
	baseURL string
	apiKey  string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
	a.apiKey = info.ApiKey
}

func requestObject(c *gin.Context) (map[string]any, error) {
	var body map[string]any
	if err := common.UnmarshalBodyReusable(c, &body); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, fmt.Errorf("JSON object is required")
	}
	return body, nil
}

func numericField(body map[string]any, key string) (float64, bool) {
	value, exists := body[key]
	if !exists {
		return 0, false
	}
	switch number := value.(type) {
	case float64:
		return number, true
	case int:
		return float64(number), true
	default:
		return 0, false
	}
}

func billingModel(modelName, resolution string) string {
	modelID := "aux/" + modelName
	modelFamily := strings.ToLower(modelName)
	resolution = strings.ToLower(strings.TrimSpace(resolution))
	if resolution == "" {
		return modelID
	}
	maxResolution := "720p"
	switch {
	case modelFamily == "sora-2-pro", modelFamily == "wan2.6", modelFamily == "wan2.7", strings.HasPrefix(modelFamily, "wan3.0-video"), modelFamily == "kling-v3", modelFamily == "kling-v3-omni", modelFamily == "kling-v2-6", modelFamily == "kling-video-o1", strings.HasPrefix(modelFamily, "minimax-hailuo"):
		maxResolution = "1080p"
	case strings.HasPrefix(modelFamily, "minimax-h3"):
		maxResolution = "2k"
	}
	if resolution != maxResolution {
		return modelID + "-" + resolution
	}
	return modelID
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	if c.Param("kind") == "image" && strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/form-data") {
		form, err := common.ParseMultipartFormReusable(c)
		if err != nil {
			return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
		}
		modelName := strings.TrimSpace(strings.Join(form.Value["model"], ""))
		prompt := strings.TrimSpace(strings.Join(form.Value["prompt"], ""))
		if modelName == "" || prompt == "" || len(form.File["image"]) != 1 {
			return service.TaskErrorWrapperLocal(fmt.Errorf("model, prompt and one image are required"), "invalid_request", http.StatusBadRequest)
		}
		if n := strings.TrimSpace(strings.Join(form.Value["n"], "")); n != "" && n != "1" {
			return service.TaskErrorWrapperLocal(fmt.Errorf("one image per task is required"), "invalid_request", http.StatusBadRequest)
		}
		info.Action = "clawhorseImageEdit"
		priceModel := modelName
		if modelName == "grok-imagine-1.5-edit-apimart" {
			priceModel = "grok-imagine-1.5-apimart"
		}
		info.OriginModelName = "aux/" + priceModel
		if _, priced := ratio_setting.GetModelPrice(info.OriginModelName, true); !priced {
			return service.TaskErrorWrapperLocal(fmt.Errorf("official per-image price is not configured for %s", info.OriginModelName), "model_price_not_configured", http.StatusBadRequest)
		}
		return nil
	}
	body, err := requestObject(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	modelName, _ := body["model"].(string)
	modelName = strings.TrimSpace(modelName)
	prompt, _ := body["prompt"].(string)
	if modelName == "" || strings.TrimSpace(prompt) == "" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("model and prompt are required"), "invalid_request", http.StatusBadRequest)
	}
	kind := c.Param("kind")
	if kind != "image" && kind != "video" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("kind must be image or video"), "invalid_request", http.StatusBadRequest)
	}
	if kind == "image" {
		if n, exists := numericField(body, "n"); exists && n != 1 {
			return service.TaskErrorWrapperLocal(fmt.Errorf("one image per task is required"), "invalid_request", http.StatusBadRequest)
		}
		info.Action = "clawhorseImage"
	} else {
		seconds, hasSeconds := numericField(body, "seconds")
		duration, hasDuration := numericField(body, "duration")
		if !hasSeconds && !hasDuration {
			return service.TaskErrorWrapperLocal(fmt.Errorf("duration is required"), "invalid_request", http.StatusBadRequest)
		}
		if hasSeconds && hasDuration && seconds != duration {
			return service.TaskErrorWrapperLocal(fmt.Errorf("seconds and duration disagree"), "invalid_request", http.StatusBadRequest)
		}
		if !hasDuration {
			duration = seconds
		}
		if duration < 1 || duration > 60 || duration != float64(int(duration)) {
			return service.TaskErrorWrapperLocal(fmt.Errorf("duration must be an integer from 1 to 60"), "invalid_request", http.StatusBadRequest)
		}
		c.Set("clawhorse_apib_seconds", duration)
		info.Action = "clawhorseVideo"
	}
	c.Set("clawhorse_apib_model", modelName)
	resolution, _ := body["resolution"].(string)
	info.OriginModelName = billingModel(modelName, resolution)
	_, priced := ratio_setting.GetModelPrice(info.OriginModelName, true)
	_, metered, _ := ratio_setting.GetModelRatio(info.OriginModelName)
	if !priced && !metered {
		return service.TaskErrorWrapperLocal(fmt.Errorf("official price is not configured for %s", info.OriginModelName), "model_price_not_configured", http.StatusBadRequest)
	}
	return nil
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	if info.Action != "clawhorseVideo" || info.PriceData.UsePrice {
		return nil
	}
	seconds := c.GetFloat64("clawhorse_apib_seconds")
	// ModelRatio prices aux media in 1000 synthetic tokens per second. The
	// task framework's base quota is ratio/2 * QuotaPerUnit, so this factor
	// yields ratio * 1000 * seconds without introducing a second price table.
	return map[string]float64{"seconds": seconds * 2000 / float64(common.QuotaPerUnit)}
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.Action == "clawhorseImageEdit" {
		return a.baseURL + "/v1/images/edits", nil
	}
	if info.Action == "clawhorseImage" {
		return a.baseURL + "/v1/images/generations", nil
	}
	if info.Action == "clawhorseVideo" {
		return a.baseURL + "/v1/videos/generations", nil
	}
	return "", fmt.Errorf("unknown ClawHorse media task action")
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", c.GetHeader("Content-Type"))
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, _ *relaycommon.RelayInfo) (io.Reader, error) {
	if strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/form-data") {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return nil, err
		}
		return common.NewReplayableBodyReader(storage), nil
	}
	body, err := requestObject(c)
	if err != nil {
		return nil, err
	}
	body["model"] = c.GetString("clawhorse_apib_model")
	encoded, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(encoded), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func submittedID(body map[string]any) string {
	if id, _ := body["task_id"].(string); id != "" {
		return id
	}
	if data, ok := body["data"].(map[string]any); ok {
		if id, _ := data["task_id"].(string); id != "" {
			return id
		}
	}
	if data, ok := body["data"].([]any); ok && len(data) > 0 {
		if first, ok := data[0].(map[string]any); ok {
			id, _ := first["task_id"].(string)
			return id
		}
	}
	return ""
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (string, []byte, *dto.TaskError) {
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_failed", http.StatusBadGateway)
	}
	var body map[string]any
	if err := common.Unmarshal(raw, &body); err != nil {
		return "", nil, service.TaskErrorWrapper(err, "invalid_upstream_response", http.StatusBadGateway)
	}
	if code, exists := numericField(body, "code"); exists && code != 200 {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("upstream rejected task: code %.0f", code), "upstream_rejected_task", http.StatusBadGateway)
	}
	id := submittedID(body)
	if id == "" {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("upstream returned no task ID"), "invalid_upstream_response", http.StatusBadGateway)
	}
	c.JSON(http.StatusOK, gin.H{"task_id": info.PublicTaskID, "status": "submitted"})
	return id, raw, nil
}

func (a *TaskAdaptor) GetModelList() []string { return nil }
func (a *TaskAdaptor) GetChannelName() string { return "ClawHorse APIB tasks" }

func (a *TaskAdaptor) FetchTask(baseURL, key string, body map[string]any, proxy string) (*http.Response, error) {
	id, _ := body["task_id"].(string)
	if id == "" {
		return nil, fmt.Errorf("task_id is required")
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(baseURL, "/")+"/v1/tasks/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, err
	}
	return client.Do(req)
}

func resultURL(value any, depth int) string {
	if depth > 6 {
		return ""
	}
	switch item := value.(type) {
	case string:
		if strings.HasPrefix(item, "https://") || strings.HasPrefix(item, "http://") {
			return item
		}
	case []any:
		for _, child := range item {
			if found := resultURL(child, depth+1); found != "" {
				return found
			}
		}
	case map[string]any:
		for _, field := range []string{"result", "videos", "images", "url", "output", "video_url", "image_url"} {
			if found := resultURL(item[field], depth+1); found != "" {
				return found
			}
		}
	}
	return ""
}

func (a *TaskAdaptor) ParseTaskResult(raw []byte) (*relaycommon.TaskInfo, error) {
	var body map[string]any
	if err := common.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	data, _ := body["data"].(map[string]any)
	if data == nil {
		return nil, fmt.Errorf("task status has no data")
	}
	status, _ := data["status"].(string)
	result := &relaycommon.TaskInfo{}
	switch strings.ToLower(status) {
	case "submitted":
		result.Status = string(model.TaskStatusSubmitted)
	case "pending", "queued":
		result.Status = string(model.TaskStatusQueued)
	case "processing", "in_progress", "running":
		result.Status = string(model.TaskStatusInProgress)
	case "completed", "succeeded", "success":
		result.Status = string(model.TaskStatusSuccess)
		result.Url = resultURL(data["result"], 0)
		if result.Url == "" {
			return nil, fmt.Errorf("completed task has no output URL")
		}
	case "failed", "failure", "error", "cancelled", "canceled":
		result.Status = string(model.TaskStatusFailure)
		result.Reason, _ = data["fail_reason"].(string)
		if failure, ok := data["error"].(map[string]any); ok {
			if message, _ := failure["message"].(string); message != "" {
				result.Reason = message
			}
		}
	default:
		return nil, fmt.Errorf("unknown task status %q", status)
	}
	return result, nil
}
