package openai

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSemanticUpstreamFailure(t *testing.T) {
	t.Run("retries an overload envelope incorrectly delivered as assistant content", func(t *testing.T) {
		err := semanticUpstreamFailure(`{"error":{"code":"overloaded","type":"server_error","message":"The upstream server is overloaded, please retry later."}}`)
		require.NotNil(t, err)
		require.Equal(t, http.StatusServiceUnavailable, err.StatusCode)
		require.Equal(t, "upstream_transient_envelope", string(err.GetErrorCode()))
	})

	t.Run("does not retry a valid assistant JSON result", func(t *testing.T) {
		require.Nil(t, semanticUpstreamFailure(`{"title":"正常方案","shots":[]}`))
	})

	t.Run("does not retry an assistant discussing an error", func(t *testing.T) {
		require.Nil(t, semanticUpstreamFailure(`{"error":{"code":"validation_error","message":"用户要求我解释服务过载时的处理方式"}}`))
	})
}
