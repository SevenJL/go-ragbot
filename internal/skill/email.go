package skill

import (
	"context"
	"fmt"
	"net/smtp"
	"strings"

	"ragbot/internal/config"
	"ragbot/internal/session"
)

// EmailSkill walks the user through composing and sending an email.
//
// Steps: 0=recipient, 1=subject, 2=body, 3=confirm.
type EmailSkill struct {
	cfg config.EmailConfig
}

func NewEmailSkill(cfg config.EmailConfig) *EmailSkill { return &EmailSkill{cfg: cfg} }

func (s *EmailSkill) Name() string        { return "email" }
func (s *EmailSkill) Description() string { return "多轮引导：发送邮件" }

func (s *EmailSkill) Match(input string) bool {
	return containsAny(input, []string{"发邮件", "发送邮件", "写邮件", "send email", "send an email"})
}

func (s *EmailSkill) Start(ctx context.Context, sess *session.Session) (string, error) {
	sess.StartSkill(s.Name())
	return "好的，我们来发一封邮件。请问收件人邮箱是？（随时输入“取消”可退出）", nil
}

func (s *EmailSkill) Handle(ctx context.Context, sess *session.Session, input string) (string, bool, error) {
	input = strings.TrimSpace(input)
	if isCancel(input) {
		sess.EndSkill()
		return "已取消发送邮件，回到知识库问答模式。", true, nil
	}

	switch sess.SkillStep {
	case 0: // recipient
		if !strings.Contains(input, "@") {
			return "这看起来不是有效邮箱，请重新输入收件人邮箱：", false, nil
		}
		sess.SkillData["to"] = input
		sess.SkillStep = 1
		return "收件人已记录。邮件主题是？", false, nil

	case 1: // subject
		sess.SkillData["subject"] = input
		sess.SkillStep = 2
		return "主题已记录。请输入邮件正文：", false, nil

	case 2: // body
		sess.SkillData["body"] = input
		sess.SkillStep = 3
		preview := fmt.Sprintf(
			"请确认以下邮件：\n收件人：%s\n主题：%s\n正文：%s\n\n回复“确认”发送，或“取消”放弃。",
			sess.SkillData["to"], sess.SkillData["subject"], sess.SkillData["body"])
		return preview, false, nil

	case 3: // confirm
		if !containsAny(input, []string{"确认", "发送", "yes", "confirm", "send"}) {
			return "未识别为确认。回复“确认”发送，或“取消”放弃。", false, nil
		}
		err := s.send(sess.SkillData)
		sess.EndSkill()
		if err != nil {
			return "发送失败：" + err.Error() + "\n已回到知识库问答模式。", true, nil
		}
		return "✅ 邮件已发送。回到知识库问答模式。", true, nil
	}

	sess.EndSkill()
	return "邮件流程异常，已重置。", true, nil
}

func (s *EmailSkill) send(data map[string]string) error {
	// If SMTP isn't configured, simulate so the demo works offline.
	if s.cfg.SMTPHost == "" || s.cfg.Username == "" {
		return fmt.Errorf("（未配置 SMTP，模拟发送）邮件内容已准备好，配置 skills.email 后即可真实发送")
	}
	from := s.cfg.From
	if from == "" {
		from = s.cfg.Username
	}
	msg := []byte(fmt.Sprintf("To: %s\r\nFrom: %s\r\nSubject: %s\r\n\r\n%s\r\n",
		data["to"], from, data["subject"], data["body"]))
	auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.SMTPHost)
	addr := fmt.Sprintf("%s:%d", s.cfg.SMTPHost, s.cfg.SMTPPort)
	return smtp.SendMail(addr, auth, from, []string{data["to"]}, msg)
}
