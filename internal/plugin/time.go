package plugin

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// TimePlugin answers "what time / date is it" style questions directly,
// short-circuiting the RAG pipeline.
type TimePlugin struct {
	base
}

func NewTimePlugin(enabled bool) *TimePlugin {
	p := &TimePlugin{}
	p.SetEnabled(enabled)
	return p
}

func (p *TimePlugin) Name() string        { return "time" }
func (p *TimePlugin) Description() string { return "回答当前时间/日期相关的问题" }

var timeKeywords = []string{
	"现在几点", "几点了", "现在时间", "当前时间", "今天日期", "今天几号",
	"今天星期几", "现在是几号", "what time", "current time", "today's date",
	"what date", "what day",
}

func (p *TimePlugin) matches(q string) bool {
	lq := strings.ToLower(q)
	for _, k := range timeKeywords {
		if strings.Contains(lq, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

func (p *TimePlugin) BeforeRAG(ctx context.Context, query string) (*Result, error) {
	if !p.matches(query) {
		return nil, nil
	}
	now := time.Now()
	weekdayCN := []string{"日", "一", "二", "三", "四", "五", "六"}[int(now.Weekday())]
	answer := fmt.Sprintf("现在是 %s（星期%s），时区 %s。",
		now.Format("2006-01-02 15:04:05"), weekdayCN, now.Format("MST"))
	return &Result{Handled: true, Answer: answer}, nil
}

func (p *TimePlugin) AfterRAG(ctx context.Context, query, answer string) (*Result, error) {
	return nil, nil
}
