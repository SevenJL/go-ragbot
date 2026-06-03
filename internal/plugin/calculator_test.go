package plugin

import (
	"context"
	"strings"
	"testing"
)

func TestCalculatorPluginEvaluatesExpression(t *testing.T) {
	p := NewCalculatorPlugin(true)
	res, err := p.BeforeRAG(context.Background(), "计算(3+4)*5")
	if err != nil {
		t.Fatal(err)
	}
	if res == nil || !res.Handled {
		t.Fatal("expected calculator to handle expression")
	}
	if !strings.Contains(res.Answer, "35") {
		t.Fatalf("answer = %q", res.Answer)
	}
}

func TestCalculatorPluginIgnoresInvalidInput(t *testing.T) {
	p := NewCalculatorPlugin(true)
	res, err := p.BeforeRAG(context.Background(), "please explain apples")
	if err != nil {
		t.Fatal(err)
	}
	if res != nil {
		t.Fatalf("expected nil result, got %#v", res)
	}
}
