package zutils

import (
	"errors"
	"io"
	"net/http"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/vcaesar/zedsearch/test/utils"
)

func TestBindJSON(t *testing.T) {
	type Request struct {
		Name string `json:"name"`
	}

	t.Run("decodes the body", func(t *testing.T) {
		c, _ := utils.NewContext()
		utils.SetRequestData(c, `{"name":"zed"}`)
		var req Request
		assert.NoError(t, BindJSON(c, &req))
		assert.Equal(t, Request{Name: "zed"}, req)
	})

	t.Run("invalid json", func(t *testing.T) {
		c, _ := utils.NewContext()
		utils.SetRequestData(c, `{"name":`)
		var req Request
		assert.Error(t, BindJSON(c, &req))
		assert.Empty(t, req.Name)
	})

	t.Run("missing body", func(t *testing.T) {
		c, _ := utils.NewContext()
		var req Request
		assert.Error(t, BindJSON(c, &req))
	})

	t.Run("body read error", func(t *testing.T) {
		errRead := errors.New("connection reset")
		c, _ := utils.NewContext()
		c.Request.Body = io.NopCloser(iotest.ErrReader(errRead))
		var req Request
		assert.ErrorIs(t, BindJSON(c, &req), errRead)
	})
}

func TestGetRenderer(t *testing.T) {
	type args struct {
		qparams map[string]string
	}
	type want struct {
		pretty bool
	}
	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "pretty without value should use IndentedJSON",
			args: args{
				qparams: map[string]string{"pretty": ""},
			},
			want: want{pretty: true},
		},
		{
			name: "param pretty with true value should use IndentedJSON",
			args: args{
				qparams: map[string]string{"pretty": "true"},
			},
			want: want{pretty: true},
		},
		{
			name: "param pretty with true value should be case insensitive",
			args: args{
				qparams: map[string]string{"pretty": "TrUe"},
			},
			want: want{pretty: true},
		},
		{
			name: "param pretty with any other value then empty or true should use JSON",
			args: args{
				qparams: map[string]string{"pretty": "No"},
			},
			want: want{pretty: false},
		},
		{
			name: "param pretty missing should use JSON",
			args: args{
				qparams: map[string]string{},
			},
			want: want{pretty: false},
		},
	}
	type Response struct {
		Name string `json:"name"`
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := utils.NewContext()
			utils.SetRequestURL(c, "/", tt.args.qparams)
			RenderJSON(c, http.StatusOK, Response{Name: "zed"})
			if tt.want.pretty {
				assert.Contains(t, w.Body.String(), "\n")
			} else {
				assert.NotContains(t, w.Body.String(), "\n")
			}
		})
	}
}
