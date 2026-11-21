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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConvert_OtherTypes(t *testing.T) {
	t.Run("ToString formats any other value", func(t *testing.T) {
		got, err := ToString([]int{1, 2})
		assert.NoError(t, err)
		assert.Equal(t, "[1 2]", got)
	})

	t.Run("false is zero", func(t *testing.T) {
		f, err := ToFloat64(false)
		assert.NoError(t, err)
		assert.Equal(t, 0.0, f)
		u, err := ToUint64(false)
		assert.NoError(t, err)
		assert.Equal(t, uint64(0), u)
		i, err := ToInt(false)
		assert.NoError(t, err)
		assert.Equal(t, 0, i)
	})

	t.Run("zero is false", func(t *testing.T) {
		for _, v := range []interface{}{0.0, uint64(0), int64(0), 0, "false"} {
			got, err := ToBool(v)
			assert.NoError(t, err)
			assert.False(t, got, "%T", v)
		}
	})

	t.Run("unsupported type", func(t *testing.T) {
		_, err := ToFloat64(nil)
		assert.EqualError(t, err, "ToFloat64: unknown supported type <nil>")
		_, err = ToUint64([]int{1})
		assert.EqualError(t, err, "ToInt: unknown supported type []int")
		_, err = ToInt(int32(1))
		assert.EqualError(t, err, "ToInt: unknown supported type int32")
		_, err = ToBool(nil)
		assert.EqualError(t, err, "ToInt: unknown supported type <nil>")
	})

	t.Run("string that is not a number", func(t *testing.T) {
		_, err := ToFloat64("x")
		assert.Error(t, err)
		_, err = ToUint64("-1")
		assert.Error(t, err)
		_, err = ToInt("1.5")
		assert.Error(t, err)
		_, err = ToBool("maybe")
		assert.Error(t, err)
	})
}

func TestToString(t *testing.T) {
	type args struct {
		v interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr bool
	}{
		{
			name: "string",
			args: args{
				v: "test",
			},
			want: "test",
		},
		{
			name: "float64",
			args: args{
				v: 3.14,
			},
			want: "3.14",
		},
		{
			name: "uint64",
			args: args{
				v: uint64(3),
			},
			want: "3",
		},
		{
			name: "int64",
			args: args{
				v: int64(3),
			},
			want: "3",
		},
		{
			name: "int",
			args: args{
				v: int(3),
			},
			want: "3",
		},
		{
			name: "bool",
			args: args{
				v: true,
			},
			want: "true",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToString(tt.args.v)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ToString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToFloat64(t *testing.T) {
	type args struct {
		v interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    float64
		wantErr bool
	}{
		{
			name: "string",
			args: args{
				v: "3.14",
			},
			want: 3.14,
		},
		{
			name: "float64",
			args: args{
				v: 3.14,
			},
			want: 3.14,
		},
		{
			name: "uint64",
			args: args{
				v: uint64(3),
			},
			want: float64(3),
		},
		{
			name: "int64",
			args: args{
				v: int64(3),
			},
			want: float64(3),
		},
		{
			name: "int",
			args: args{
				v: int(3),
			},
			want: float64(3),
		},
		{
			name: "bool",
			args: args{
				v: true,
			},
			want: float64(1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToFloat64(tt.args.v)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToFloat64() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ToFloat64() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToUint64(t *testing.T) {
	type args struct {
		v interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    uint64
		wantErr bool
	}{
		{
			name: "string",
			args: args{
				v: "3",
			},
			want: uint64(3),
		},
		{
			name: "float64",
			args: args{
				v: 3.14,
			},
			want: uint64(3),
		},
		{
			name: "uint64",
			args: args{
				v: uint64(3),
			},
			want: uint64(3),
		},
		{
			name: "int64",
			args: args{
				v: int64(3),
			},
			want: uint64(3),
		},
		{
			name: "int",
			args: args{
				v: int(3),
			},
			want: uint64(3),
		},
		{
			name: "bool",
			args: args{
				v: true,
			},
			want: uint64(1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToUint64(tt.args.v)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToUint64() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ToUint64() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToInt(t *testing.T) {
	type args struct {
		v interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    int
		wantErr bool
	}{
		{
			name: "string",
			args: args{
				v: "3",
			},
			want: int(3),
		},
		{
			name: "float64",
			args: args{
				v: 3.14,
			},
			want: int(3),
		},
		{
			name: "uint64",
			args: args{
				v: uint64(3),
			},
			want: int(3),
		},
		{
			name: "int64",
			args: args{
				v: int64(3),
			},
			want: int(3),
		},
		{
			name: "int",
			args: args{
				v: int(3),
			},
			want: int(3),
		},
		{
			name: "bool",
			args: args{
				v: true,
			},
			want: int(1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToInt(tt.args.v)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToInt() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ToInt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestToBool(t *testing.T) {
	type args struct {
		v interface{}
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{
			name: "string",
			args: args{
				v: "true",
			},
			want: true,
		},
		{
			name: "float64",
			args: args{
				v: 3.14,
			},
			want: true,
		},
		{
			name: "uint64",
			args: args{
				v: uint64(3),
			},
			want: true,
		},
		{
			name: "int64",
			args: args{
				v: int64(3),
			},
			want: true,
		},
		{
			name: "int",
			args: args{
				v: int(3),
			},
			want: true,
		},
		{
			name: "bool",
			args: args{
				v: true,
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ToBool(tt.args.v)
			if (err != nil) != tt.wantErr {
				t.Errorf("ToBool() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ToBool() = %v, want %v", got, tt.want)
			}
		})
	}
}
