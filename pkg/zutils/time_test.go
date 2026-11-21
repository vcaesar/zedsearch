/* Copyright 2022 Zinc Labs Inc. and Contributors
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
 */

package zutils

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParseDuration(t *testing.T) {
	type args struct {
		s string
	}
	tests := []struct {
		name    string
		args    args
		want    time.Duration
		wantErr bool
	}{
		{
			name: "1s",
			args: args{
				s: "1s",
			},
			want:    time.Second,
			wantErr: false,
		},
		{
			name: "180s",
			args: args{
				s: "180s",
			},
			want:    time.Second * 180,
			wantErr: false,
		},
		{
			name: "1m",
			args: args{
				s: "1m",
			},
			want:    time.Minute,
			wantErr: false,
		},
		{
			name: "1h",
			args: args{
				s: "1h",
			},
			want:    time.Hour,
			wantErr: false,
		},
		{
			name: "1d",
			args: args{
				s: "1d",
			},
			want:    time.Hour * 24,
			wantErr: false,
		},
		{
			name: "1y",
			args: args{
				s: "1y",
			},
			want:    0,
			wantErr: true,
		},
		{
			name: "days must be an integer",
			args: args{
				s: "1.5d",
			},
			wantErr: true,
		},
		{
			name: "plain number is nanoseconds",
			args: args{
				s: "1500",
			},
			want: 1500 * time.Nanosecond,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDuration(tt.args.s)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatDuration(t *testing.T) {
	type args struct {
		d time.Duration
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "10s",
			args: args{
				d: time.Second * 10,
			},
			want: "10s",
		},
		{
			name: "5m",
			args: args{
				d: time.Minute * 5,
			},
			want: "5m",
		},
		{
			name: "1h",
			args: args{
				d: time.Hour * 1,
			},
			want: "1h",
		},
		{
			name: "1d",
			args: args{
				d: time.Hour * 24,
			},
			want: "1d",
		},
		{
			name: "1M",
			args: args{
				d: time.Hour * 24 * 30,
			},
			want: "1M",
		},
		{
			name: "1y",
			args: args{
				d: time.Hour * 24 * 30 * 12,
			},
			want: "1y",
		},
		{
			name: "zero",
			want: "0s",
		},
		{
			name: "every unit",
			args: args{
				d: time.Hour*24*(360+60+3) + 4*time.Hour + 5*time.Minute + 6*time.Second,
			},
			want: "1y2M3d4h5m6s",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDuration(tt.args.d)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestUnix(t *testing.T) {
	type args struct {
		n int64
	}
	tests := []struct {
		name string
		args args
		want time.Time
	}{
		{
			name: "Zero",
			args: args{
				n: 0,
			},
			want: time.Unix(0, 0),
		},
		{
			name: "Unix",
			args: args{
				n: 1652176732,
			},
			want: time.Unix(1652176732, 0),
		},
		{
			name: "UnixMilli",
			args: args{
				n: 1652176732575,
			},
			want: time.UnixMilli(1652176732575),
		},
		{
			name: "UnixMicro",
			args: args{
				n: 1652176732575067,
			},
			want: time.UnixMicro(1652176732575067),
		},
		{
			name: "UnixNano",
			args: args{
				n: 1652176732575076000,
			},
			want: time.Unix(0, 1652176732575076000),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Unix(tt.args.n); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Unix() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseTime(t *testing.T) {
	nowStr := time.Now().Format(time.RFC3339)
	now, _ := time.Parse(time.RFC3339, nowStr)

	type args struct {
		value    interface{}
		format   string
		timeZone string
	}
	tests := []struct {
		name    string
		args    args
		want    time.Time
		wantErr bool
	}{
		{
			name: "ParseTime RFC3339",
			args: args{
				value:    now.Format(time.RFC3339),
				format:   "",
				timeZone: "",
			},
			want:    now,
			wantErr: false,
		},
		{
			name: "ParseTime RFC3339Nano",
			args: args{
				value:    now.Format(time.RFC3339Nano),
				format:   "",
				timeZone: "",
			},
			want:    now,
			wantErr: false,
		},
		{
			name: "ParseTime RFC1123Z",
			args: args{
				value:    now.Format(time.RFC1123Z),
				format:   time.RFC1123Z,
				timeZone: "",
			},
			want:    now,
			wantErr: false,
		},
		{
			name: "ParseTime RFC1123",
			args: args{
				value:    now.Format(time.RFC1123),
				format:   time.RFC1123,
				timeZone: time.Local.String(),
			},
			want:    now,
			wantErr: false,
		},
		{
			name: "ParseTime epoch_millis",
			args: args{
				value:    now.UnixNano(),
				format:   "",
				timeZone: "",
			},
			want:    now,
			wantErr: false,
		},
		{
			name: "ParseTime epoch_millis",
			args: args{
				value:    now.UnixNano(),
				format:   "epoch_millis",
				timeZone: "",
			},
			want:    now,
			wantErr: false,
		},
		{
			name: "ParseTime epoch_millis",
			args: args{
				value:    fmt.Sprintf("%d", now.UnixNano()),
				format:   "epoch_millis",
				timeZone: "",
			},
			want:    now,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTime(tt.args.value, tt.args.format, tt.args.timeZone)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.False(t, got.IsZero())
			assert.Equal(t, tt.want.UnixNano(), got.UnixNano())
		})
	}
}

func TestParseTime_Numeric(t *testing.T) {
	want := time.Unix(1652176732, 0)
	for _, value := range []interface{}{float64(1652176732), int64(1652176732), 1652176732, int32(1652176732)} {
		got, err := ParseTime(value, "", "")
		assert.NoError(t, err, "%T", value)
		assert.True(t, want.Equal(got), "%T", value)
	}

	got, err := ParseTime(0, "", "")
	assert.NoError(t, err)
	assert.True(t, time.Unix(0, 0).Equal(got))
}

func TestParseTime_TimeZone(t *testing.T) {
	layout := "2006-01-02 15:04:05"
	value := "2024-03-01 09:00:00"
	tests := []struct {
		name     string
		timeZone string
		want     time.Time
	}{
		{name: "default is UTC", timeZone: "", want: time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)},
		{name: "utc", timeZone: "utc", want: time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)},
		{name: "system", timeZone: "system", want: time.Date(2024, 3, 1, 9, 0, 0, 0, time.Local)},
		{name: "named", timeZone: "Asia/Tokyo", want: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTime(value, layout, tt.timeZone)
			assert.NoError(t, err)
			assert.True(t, tt.want.Equal(got), "got %s", got)
		})
	}
}

func TestParseTime_Errors(t *testing.T) {
	tests := []struct {
		name     string
		value    interface{}
		format   string
		timeZone string
		want     string
	}{
		{name: "unsupported type", value: true, want: "value type of time must be string or numeric, got bool"},
		{name: "zero time", value: time.Time{}.Unix(), want: "value [-62135596800] is not a valid timestamp"},
		{name: "empty string", value: "", want: "time value is empty"},
		{name: "epoch_millis not a number", value: "abc", format: "epoch_millis", want: "time format is [epoch_millis] but value [abc] cannot be converted to int"},
		{name: "unknown time zone", value: "2024-03-01T00:00:00Z", timeZone: "Mars/Phobos", want: "invalid time zone: Mars/Phobos"},
		{name: "value does not match the format", value: "2024-03-01", want: "time format [" + time.RFC3339 + "] value [2024-03-01] parse error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTime(tt.value, tt.format, tt.timeZone)
			assert.ErrorContains(t, err, tt.want)
			assert.True(t, got.IsZero())
		})
	}
}
