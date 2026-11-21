package zutils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalculateMin(t *testing.T) {
	cases := []struct {
		subCount int
		value    interface{}
		want     int
	}{
		// Simple Integer
		{subCount: 5, value: 3, want: 3},
		{subCount: 10, value: "2", want: 2},
		{subCount: 8, value: int64(7), want: 7},
		{subCount: 9, value: 3.0, want: 3},
		{subCount: 5, value: 5.7, want: 5},

		{subCount: 5, value: -10, want: 1},
		{subCount: 3, value: 5, want: 3},

		// Negative Integer
		{subCount: 10, value: -2, want: 8},
		{subCount: 8, value: "-5", want: 3},
		{subCount: 15, value: -3.0, want: 12},
		{subCount: 9, value: -3.5, want: 5},

		// percent
		{subCount: 10, value: "80%", want: 8},
		{subCount: 10, value: "-20%", want: 8},
		{subCount: 5, value: "75%", want: 3},
		{subCount: 5, value: "-25%", want: 4},

		// combination
		{subCount: 4, value: "5<90%", want: 4},
		{subCount: 5, value: "5<90%", want: 5},
		{subCount: 7, value: "5<3", want: 3},
		{subCount: 5, value: "2<-25%", want: 4},

		// multi combinations
		{subCount: 2, value: "2<-25% 9<-3", want: 2},
		{subCount: 5, value: "4<-25% 9<-3", want: 4},
		{subCount: 10, value: "4<-40% 9<-3", want: 7},
		{subCount: 10, value: []string{"9<-3", "4<-40%"}, want: 7},
		{subCount: 6, value: []string{"9<-3", "4<-40%"}, want: 4},

		// without sub queries the value is not read
		{subCount: 0, value: "abc", want: 1},
	}
	for _, c := range cases {
		v, err := CalculateMin(c.subCount, c.value)
		assert.Nil(t, err)
		assert.Equal(t, c.want, v)
	}
}

func TestCalculateMin_Errors(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
		want  string
	}{
		{name: "unsupported type", value: true, want: "invalid MinimumShouldMatch value"},
		{name: "not an expression", value: "abc", want: "invalid MinimumShould value: abc"},
		{name: "bad percent", value: "x%", want: "invalid MinimumShould value: x%"},
		{name: "condition overflow", value: "99999999999999999999<1", want: "cannot parse the condition value"},
		{name: "count overflow", value: "5<99999999999999999999", want: "cannot parse the clauses count"},
		{name: "bad combination", value: "abc def", want: "invalid MinimumShould value: [abc def]"},
		{name: "combination condition overflow", value: []string{"99999999999999999999<1"}, want: "cannot parse the condition value"},
		{name: "combination count overflow", value: []string{"5<99999999999999999999%"}, want: "cannot parse the clauses count"},
		{name: "no combinations", value: []string{}, want: "invalid MinimumShould value: []"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CalculateMin(5, tt.value)
			assert.ErrorContains(t, err, tt.want)
			assert.Zero(t, got)
		})
	}
}
