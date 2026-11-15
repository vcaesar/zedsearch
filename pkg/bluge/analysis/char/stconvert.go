package char

import (
	"github.com/go-ego/gocc"
)

type STConvertCharFilter struct {
	cc *gocc.Converter
}

func NewSTConvertCharFilter(conversion string) (*STConvertCharFilter, error) {
	cc, err := gocc.New(conversion)
	if err != nil {
		return nil, err
	}
	return &STConvertCharFilter{cc: cc}, nil
}

func (t STConvertCharFilter) Filter(input []byte) []byte {
	return t.cc.AppendConvert(nil, input)
}
