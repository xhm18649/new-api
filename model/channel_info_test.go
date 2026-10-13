package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelInfoScanAcceptsDatabaseJSONRepresentations(t *testing.T) {
	jsonText := `{"is_multi_key":true,"multi_key_size":2,"multi_key_polling_index":1,"multi_key_mode":"polling"}`
	for _, value := range []any{[]byte(jsonText), jsonText} {
		var info ChannelInfo
		require.NoError(t, info.Scan(value))
		require.True(t, info.IsMultiKey)
		require.Equal(t, 2, info.MultiKeySize)
		require.Equal(t, 1, info.MultiKeyPollingIndex)
	}
}

func TestChannelInfoScanAcceptsNullAndEmptyValues(t *testing.T) {
	for _, value := range []any{nil, []byte{}, "", "null"} {
		info := ChannelInfo{IsMultiKey: true}
		require.NoError(t, info.Scan(value))
		require.False(t, info.IsMultiKey)
	}
}
