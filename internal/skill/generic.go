package skill

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"ragbot/internal/session"
)

// StepDef describes one step in a configurable multi-turn skill flow.
//
// When validation is not "any" the step will re-prompt the user until the input
// passes. The value is stored under Key in the session SkillData map.
type StepDef struct {
	Prompt     string `json:"prompt"`     // shown to the user
	Key        string `json:"key"`        // key in SkillData
	Validation string `json:"validation"` // "nonempty" | "email" | "number" | "any" (default "nonempty")
}

// SkillDef is the JSON schema for defining a multi-turn skill at runtime.
//
//	{
//	  "name": "order-lunch",
//	  "description": "帮大家订餐",
//	  "triggers": ["订餐", "点外卖"],
//	  "steps": [
//	    {"prompt": "谁要订餐？", "key": "who"},
//	    {"prompt": "点什么菜？", "key": "dish"}
//	  ],
//	  "finish_prompt": "确认：{who} 点了 {dish}",
//	  "finish_message": "订餐完成！"
//	}
//
// The placeholders {key} in finish_prompt / finish_message are expanded from
// the collected SkillData before showing to the user.
type SkillDef struct {
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Triggers      []string  `json:"triggers"`
	Steps         []StepDef `json:"steps"`
	FinishPrompt  string    `json:"finish_prompt"`
	FinishMessage string    `json:"finish_message"`
	CancelMessage string    `json:"cancel_message"`
}

// Validate checks the definition for required fields and returns any errors.
func (d *SkillDef) Validate() error {
	if d.Name == "" {
		return fmt.Errorf("skill name is required")
	}
	if len(d.Steps) == 0 {
		return fmt.Errorf("at least one step is required")
	}
	if d.FinishMessage == "" {
		d.FinishMessage = fmt.Sprintf("【%s】已完成，回到知识库问答模式。", d.Name)
	}
	if d.CancelMessage == "" {
		d.CancelMessage = "已取消，回到知识库问答模式。"
	}
	if d.Description == "" {
		d.Description = d.Name
	}
	for i, st := range d.Steps {
		if st.Key == "" {
			return fmt.Errorf("step %d: key is required", i)
		}
		if st.Prompt == "" {
			return fmt.Errorf("step %d (%s): prompt is required", i, st.Key)
		}
		if st.Validation == "" {
			d.Steps[i].Validation = "nonempty"
		}
		switch d.Steps[i].Validation {
		case "nonempty", "email", "number", "any":
			// ok
		default:
			return fmt.Errorf("step %d (%s): unknown validation %q", i, st.Key, st.Validation)
		}
	}
	return nil
}

// ConfigurableSkill is a Skill fully defined by a SkillDef JSON. Users can
// register these at runtime without writing Go code.
type ConfigurableSkill struct {
	def SkillDef
}

// NewConfigurableSkill creates a runtime-defined skill from a validated def.
func NewConfigurableSkill(def SkillDef) (*ConfigurableSkill, error) {
	if err := def.Validate(); err != nil {
		return nil, fmt.Errorf("invalid skill definition: %w", err)
	}
	return &ConfigurableSkill{def: def}, nil
}

func (s *ConfigurableSkill) Name() string        { return s.def.Name }
func (s *ConfigurableSkill) Description() string { return s.def.Description }

func (s *ConfigurableSkill) Match(input string) bool {
	return containsAny(input, s.def.Triggers)
}

func (s *ConfigurableSkill) Start(ctx context.Context, sess *session.Session) (string, error) {
	sess.StartSkill(s.def.Name)
	return s.def.Steps[0].Prompt, nil
}

func (s *ConfigurableSkill) Handle(ctx context.Context, sess *session.Session, input string) (string, bool, error) {
	input = strings.TrimSpace(input)
	if isCancel(input) {
		sess.EndSkill()
		return s.def.CancelMessage, true, nil
	}

	stepIdx := sess.SkillStep
	if stepIdx < 0 || stepIdx >= len(s.def.Steps) {
		sess.EndSkill()
		return "流程异常，已重置。", true, nil
	}

	// If this is the confirm step (after all data collected), handle confirm/retry.
	if stepIdx == len(s.def.Steps) {
		return s.handleConfirm(sess, input)
	}

	// Validate input for current step.
	step := s.def.Steps[stepIdx]
	if msg, ok := validateInput(input, step.Validation); !ok {
		return msg, false, nil
	}
	sess.SkillData[step.Key] = input
	sess.SkillStep++

	// If more steps remain, prompt for the next one.
	if sess.SkillStep < len(s.def.Steps) {
		return s.def.Steps[sess.SkillStep].Prompt, false, nil
	}

	// All data collected — show confirmation.
	return s.expand(s.def.FinishPrompt, sess.SkillData), false, nil
}

func (s *ConfigurableSkill) handleConfirm(sess *session.Session, input string) (string, bool, error) {
	input = strings.ToLower(strings.TrimSpace(input))
	switch {
	case containsAny(input, []string{"确认", "是", "yes", "confirm", "ok", "好", "可以"}):
		msg := s.expand(s.def.FinishMessage, sess.SkillData)
		sess.EndSkill()
		return msg, true, nil
	case containsAny(input, []string{"重来", "重新填写", "重新输入", "redo", "reset", "restart", "修改"}):
		sess.SkillStep = 0
		sess.SkillData = map[string]string{}
		return "好的，我们重新开始。" + s.def.Steps[0].Prompt, false, nil
	default:
		return "请回复“确认”执行，或回复“重来”重新填写。", false, nil
	}
}

// expand replaces {key} placeholders in s with values from data.
func (s *ConfigurableSkill) expand(tmpl string, data map[string]string) string {
	for k, v := range data {
		tmpl = strings.ReplaceAll(tmpl, "{"+k+"}", v)
	}
	return tmpl
}

var emailRE = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// validateInput checks value against rule. Returns (retryMessage, ok).
func validateInput(value, rule string) (string, bool) {
	switch rule {
	case "any":
		return "", true
	case "nonempty":
		if strings.TrimSpace(value) == "" {
			return "输入不能为空，请重新输入：", false
		}
		return "", true
	case "email":
		if !emailRE.MatchString(strings.TrimSpace(value)) {
			return "邮箱格式不正确，请重新输入：", false
		}
		return "", true
	case "number":
		cleaned := strings.TrimSpace(value)
		if cleaned == "" {
			return "请输入一个数字：", false
		}
		for _, r := range cleaned {
			if r == '.' || r == '-' || r == '+' {
				continue
			}
			if r < '0' || r > '9' {
				return "输入不是有效数字，请重新输入：", false
			}
		}
		return "", true
	default:
		return "", true
	}
}
