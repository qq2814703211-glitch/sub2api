package service

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// ticketEmailRetryInterval is how often the outbox is polled when no reply wakes
// the worker. The worker still honours the persisted next_attempt_at backoff.
const ticketEmailRetryInterval = 15 * time.Second

// ticketEmailPermanentError marks failures that retrying cannot fix (missing
// recipient, invalid address, deleted account). The outbox records them once and
// stops instead of hammering SMTP.
type ticketEmailPermanentError struct{ err error }

func (e ticketEmailPermanentError) Error() string         { return e.err.Error() }
func (e ticketEmailPermanentError) Unwrap() error         { return e.err }
func (e ticketEmailPermanentError) TicketEmailPermanent() {}

func ticketEmailPermanent(err error) error {
	if err == nil {
		return nil
	}
	return ticketEmailPermanentError{err: err}
}

// IsTicketEmailPermanent lets the outbox stop retrying failures that cannot
// succeed later (missing recipient, invalid address, deleted account).
func IsTicketEmailPermanent(err error) bool {
	var permanent interface{ TicketEmailPermanent() }
	return errors.As(err, &permanent)
}

// ProvideTicketService wires the repository, user lookup, mail transport and the
// shared notification-locale resolver, then starts the durable outbox worker.
// Passing nil email/settings keeps the service inert (unit tests, jobs disabled).
func ProvideTicketService(repo TicketRepository, users UserRepository, settings SettingRepository, email *EmailService, notifications *NotificationEmailService) *TicketService {
	svc := NewTicketService(repo, users)
	svc.settings = settings
	svc.email = email
	svc.notificationEmails = notifications
	if repo != nil && settings != nil && email != nil {
		svc.startEmailWorker()
	}
	return svc
}

func (s *TicketService) startEmailWorker() {
	s.emailWake = make(chan struct{}, 1)
	s.stop = make(chan struct{})
	s.stopped = make(chan struct{})
	s.workerStarted = true
	go s.runEmailWorker()
}

// Stop terminates the outbox worker. It is safe to call more than once.
func (s *TicketService) Stop() {
	if s == nil || !s.workerStarted || s.stop == nil {
		return
	}
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	select {
	case <-s.stopped:
	case <-time.After(5 * time.Second):
	}
}

func (s *TicketService) wakeEmailWorker() {
	if s == nil || s.emailWake == nil {
		return
	}
	select {
	case s.emailWake <- struct{}{}:
	default:
	}
}

func (s *TicketService) runEmailWorker() {
	defer close(s.stopped)
	ticker := time.NewTicker(ticketEmailRetryInterval)
	defer ticker.Stop()
	for {
		s.drainEmailOutbox()
		select {
		case <-s.stop:
			return
		case <-s.emailWake:
		case <-ticker.C:
		}
	}
}

func (s *TicketService) drainEmailOutbox() {
	for processedCount := 0; processedCount < 10; processedCount++ {
		select {
		case <-s.stop:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		enabled := s.emailNotificationsEnabled(ctx)
		deliver := s.sendTicketEmail
		if !enabled {
			// Notifications are switched off: drain queued rows without sending so
			// re-enabling the feature never floods users with stale mail.
			deliver = func(context.Context, TicketEmailJob) error { return nil }
		}
		processed, err := s.repo.ProcessPendingEmail(ctx, deliver)
		cancel()
		if err != nil {
			slog.Error("ticket email outbox processing failed", "error_type", fmt.Sprintf("%T", err))
			return
		}
		if !processed {
			return
		}
	}
}

func (s *TicketService) emailNotificationsEnabled(ctx context.Context) bool {
	if s.settings == nil {
		return false
	}
	raw, err := s.settings.GetValue(ctx, SettingKeyTicketEmailEnabled)
	if err != nil {
		// A missing row is the default (enabled). A real read error must not turn
		// itself into an accidental mail storm, so treat it as disabled.
		return errors.Is(err, ErrSettingNotFound)
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// sendTicketEmail renders and delivers one queued notification.
func (s *TicketService) sendTicketEmail(ctx context.Context, job TicketEmailJob) error {
	if s.email == nil {
		return ticketEmailPermanent(errors.New("email service is not configured"))
	}
	detail, err := s.repo.EmailConversation(ctx, job.TicketID, job.MessageID)
	if err != nil {
		if errors.Is(err, ErrTicketNotFound) {
			return nil // message/ticket no longer exists: nothing to deliver
		}
		return err
	}
	if detail == nil || detail.Ticket == nil || detail.Ticket.ID != job.TicketID {
		return nil
	}
	var latest *TicketMessage
	for i := range detail.Messages {
		if detail.Messages[i].ID == job.MessageID {
			latest = &detail.Messages[i]
			break
		}
	}
	if latest == nil || latest.Kind != "reply" {
		return nil
	}
	if job.Audience == "user" {
		return s.sendTicketUserEmail(ctx, detail, latest.ID)
	}
	return s.sendTicketAdminEmail(ctx, detail, latest.ID)
}

func (s *TicketService) sendTicketUserEmail(ctx context.Context, detail *TicketDetail, messageID int64) error {
	recipient, err := s.users.GetByID(ctx, detail.Ticket.UserID)
	if err != nil {
		return err
	}
	if recipient == nil || recipient.DeletedAt != nil || recipient.ID != detail.Ticket.UserID {
		return ticketEmailPermanent(ErrTicketNotFound)
	}
	if _, err := parseSMTPAddress(recipient.Email, "recipient"); err != nil {
		return ticketEmailPermanent(fmt.Errorf("ticket owner has no valid email address"))
	}
	locale := notificationEmailDefaultLocale
	if s.notificationEmails != nil {
		locale = s.notificationEmails.ResolveRecipientLocale(ctx, recipient.ID, recipient.Email)
	}
	chinese := locale == notificationEmailLocaleChinese
	site := s.siteName(ctx)
	subject := fmt.Sprintf("[%s] Ticket #%d has a new reply", site, detail.Ticket.ID)
	title := "Your ticket has a new reply"
	intro := "Our support team replied to your ticket. The conversation up to this reply is included below."
	if chinese {
		subject = fmt.Sprintf("[%s] 工单 #%d 有新回复", site, detail.Ticket.ID)
		title = "您的工单有新的回复"
		intro = "客服已回复您的工单，以下附上截至本次回复的对话记录。"
	}
	link := s.emailLink(ctx, "/tickets/"+strconv.FormatInt(detail.Ticket.ID, 10))
	body := ticketEmailHTML(ticketEmailRenderInput{
		Locale: locale, SiteName: site, Title: title, Intro: intro, Ticket: detail.Ticket,
		Messages: detail.Messages, ThroughMessageID: messageID, Link: link,
	})
	return s.email.SendEmail(ctx, recipient.Email, sanitizeEmailHeader(subject), body)
}

func (s *TicketService) sendTicketAdminEmail(ctx context.Context, detail *TicketDetail, messageID int64) error {
	recipientEmail, _, err := s.ticketAdminRecipient(ctx)
	if err != nil {
		return err
	}
	requester, err := s.users.GetByID(ctx, detail.Ticket.UserID)
	if err != nil {
		return err
	}
	if requester == nil || requester.DeletedAt != nil || requester.ID != detail.Ticket.UserID {
		return ticketEmailPermanent(ErrTicketNotFound)
	}
	site := s.siteName(ctx)
	// The support inbox is operated by the site's Chinese-speaking team.
	subject := fmt.Sprintf("[%s] 工单 #%d 有新消息", site, detail.Ticket.ID)
	title := "工单有新消息"
	intro := "用户在工单中心提交了新消息，请及时查看并回复。"
	link := s.emailLink(ctx, "/admin/tickets/"+strconv.FormatInt(detail.Ticket.ID, 10))
	body := ticketEmailHTML(ticketEmailRenderInput{
		Locale: notificationEmailLocaleChinese, SiteName: site, Title: title, Intro: intro, Ticket: detail.Ticket,
		Messages: detail.Messages, ThroughMessageID: messageID, Link: link,
		AdminView: true, RequesterName: strings.TrimSpace(requester.Username), RequesterEmail: requester.Email,
	})
	return s.email.SendEmail(ctx, recipientEmail, sanitizeEmailHeader(subject), body)
}

// ticketAdminRecipient returns the configured support inbox, or the first active
// administrator with a valid address as a fallback.
func (s *TicketService) ticketAdminRecipient(ctx context.Context) (email, name string, err error) {
	if s.settings != nil {
		raw, readErr := s.settings.GetValue(ctx, SettingKeyTicketAdminEmail)
		if readErr != nil && !errors.Is(readErr, ErrSettingNotFound) {
			return "", "", readErr
		}
		if address := strings.TrimSpace(raw); address != "" {
			if _, err := parseSMTPAddress(address, "recipient"); err != nil {
				return "", "", ticketEmailPermanent(fmt.Errorf("ticket admin email is invalid"))
			}
			return address, "Support", nil
		}
	}
	admin, err := s.users.GetFirstAdmin(ctx)
	if err != nil {
		return "", "", err
	}
	if admin == nil || admin.DeletedAt != nil || !admin.IsAdmin() {
		return "", "", ticketEmailPermanent(fmt.Errorf("no admin recipient for ticket notification"))
	}
	if _, err := parseSMTPAddress(admin.Email, "recipient"); err != nil {
		return "", "", ticketEmailPermanent(fmt.Errorf("ticket admin has no valid email address"))
	}
	return admin.Email, admin.Username, nil
}

func (s *TicketService) siteName(ctx context.Context) string {
	if s.settings == nil {
		return defaultSiteName
	}
	name, err := s.settings.GetValue(ctx, SettingKeySiteName)
	if err != nil || strings.TrimSpace(name) == "" {
		return defaultSiteName
	}
	return strings.TrimSpace(name)
}

// emailLink builds an absolute, sanitized link. Invalid or missing settings mean
// no link at all rather than a link to an attacker-controlled origin.
func (s *TicketService) emailLink(ctx context.Context, path string) string {
	if s.settings == nil {
		return ""
	}
	for _, key := range []string{SettingKeyFrontendURL, SettingKeyAPIBaseURL} {
		value, err := s.settings.GetValue(ctx, key)
		if err != nil {
			continue
		}
		parsed, err := url.Parse(strings.TrimSpace(value))
		if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			continue
		}
		parsed.Path = strings.TrimRight(parsed.Path, "/") + path
		parsed.RawPath, parsed.RawQuery, parsed.Fragment = "", "", ""
		return parsed.String()
	}
	return ""
}

// NotificationSettings reads the admin notification configuration plus outbox
// health counters.
func (s *TicketService) NotificationSettings(ctx context.Context, actor TicketActor) (*TicketNotificationSettings, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	if !actor.Admin {
		return nil, infraerrors.Forbidden("FORBIDDEN", "admin access required")
	}
	if s.settings == nil {
		return &TicketNotificationSettings{Enabled: true}, nil
	}
	out := &TicketNotificationSettings{Enabled: true}
	raw, err := s.settings.GetValue(ctx, SettingKeyTicketEmailEnabled)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return nil, err
	}
	out.Enabled = !strings.EqualFold(strings.TrimSpace(raw), "false")
	address, err := s.settings.GetValue(ctx, SettingKeyTicketAdminEmail)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return nil, err
	}
	out.AdminEmail = strings.TrimSpace(address)
	out.PendingEmail, out.FailedEmail, err = s.repo.EmailOutboxStats(ctx)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *TicketService) UpdateNotificationSettings(ctx context.Context, actor TicketActor, in TicketNotificationSettingsInput) (*TicketNotificationSettings, error) {
	if err := s.authorize(ctx, actor); err != nil {
		return nil, err
	}
	if !actor.Admin {
		return nil, infraerrors.Forbidden("FORBIDDEN", "admin access required")
	}
	if s.settings == nil {
		return nil, ticketInvalid("settings are not available")
	}
	if in.Enabled == nil && in.AdminEmail == nil {
		return nil, ticketInvalid("no notification settings supplied")
	}
	if in.Enabled != nil {
		value := "false"
		if *in.Enabled {
			value = "true"
		}
		if err := s.settings.Set(ctx, SettingKeyTicketEmailEnabled, value); err != nil {
			return nil, err
		}
	}
	if in.AdminEmail != nil {
		address := strings.TrimSpace(*in.AdminEmail)
		if address == "" {
			if err := s.settings.Delete(ctx, SettingKeyTicketAdminEmail); err != nil && !errors.Is(err, ErrSettingNotFound) {
				return nil, err
			}
		} else {
			if _, err := parseSMTPAddress(address, "recipient"); err != nil {
				return nil, ticketInvalid("admin_email must be a valid email address")
			}
			if err := s.settings.Set(ctx, SettingKeyTicketAdminEmail, address); err != nil {
				return nil, err
			}
		}
	}
	return s.NotificationSettings(ctx, actor)
}

type ticketEmailRenderInput struct {
	Locale           string
	SiteName         string
	Title            string
	Intro            string
	Ticket           *Ticket
	Messages         []TicketMessage
	ThroughMessageID int64
	Link             string
	AdminView        bool
	RequesterName    string
	RequesterEmail   string
}

// ticketEmailHTML renders one self-contained notification. Every dynamic value is
// HTML-escaped: ticket bodies are plain text, never trusted markup.
func ticketEmailHTML(in ticketEmailRenderInput) string {
	chinese := in.Locale == notificationEmailLocaleChinese
	zone := time.FixedZone("UTC+8", 8*60*60)
	roleLabel := func(m TicketMessage) string {
		switch {
		case m.Kind == "event":
			if chinese {
				return "工单动态"
			}
			return "Update"
		case in.AdminView:
			if m.AuthorRole == "admin" {
				if chinese {
					return "客服"
				}
				return "Support"
			}
			if chinese {
				return "用户"
			}
			return "User"
		default:
			if m.AuthorRole == "admin" {
				if chinese {
					return "客服"
				}
				return "Support"
			}
			if chinese {
				return "您"
			}
			return "You"
		}
	}
	var conversation strings.Builder
	for _, message := range in.Messages {
		if message.ID > in.ThroughMessageID {
			continue
		}
		label := html.EscapeString(roleLabel(message))
		if message.AuthorName != "" && message.Kind != "event" {
			label += " · " + html.EscapeString(message.AuthorName)
		}
		color, background := "#475569", "#f8fafc"
		if message.AuthorRole == "admin" && message.Kind != "event" {
			color, background = "#0f766e", "#f0fdfa"
		}
		if message.ID == in.ThroughMessageID {
			label += " · " + map[bool]string{true: "最新回复", false: "New reply"}[chinese]
		}
		content := html.EscapeString(strings.ReplaceAll(message.Content, "\r\n", "\n"))
		content = strings.ReplaceAll(content, "\n", "<br>")
		fmt.Fprintf(&conversation,
			`<table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="width:100%%;table-layout:fixed;margin:0 0 14px;border:1px solid #e2e8f0;border-radius:12px;background:%s"><tr><td style="padding:14px 16px;word-break:break-word;overflow-wrap:anywhere"><p style="margin:0 0 4px;font-size:13px;font-weight:700;color:%s">%s</p><p style="margin:0 0 10px;font-size:11px;color:#64748b">%s · UTC+8</p><div style="font-size:14px;line-height:1.75;color:#1e293b;word-break:break-word;overflow-wrap:anywhere">%s</div></td></tr></table>`,
			background, color, label, message.CreatedAt.In(zone).Format("2006-01-02 15:04"), content)
	}
	action := ""
	if in.Link != "" {
		actionLabel := "打开工单"
		if !chinese {
			actionLabel = "Open ticket"
		}
		action = fmt.Sprintf(`<p style="margin:0 0 22px"><a href="%s" style="display:inline-block;padding:11px 18px;border-radius:8px;background:#0f766e;color:#ffffff;text-decoration:none;font-size:14px;font-weight:700">%s</a></p>`,
			html.EscapeString(in.Link), actionLabel)
	}
	requester := ""
	if in.RequesterName != "" || in.RequesterEmail != "" {
		requester = fmt.Sprintf(`<p style="margin:0 0 18px;font-size:13px;line-height:1.6;color:#475569">%s · %s</p>`,
			html.EscapeString(in.RequesterName), html.EscapeString(in.RequesterEmail))
	}
	historyLabel := "对话记录"
	if !chinese {
		historyLabel = "Conversation"
	}
	footer := "此邮件由工单系统自动发送，请勿直接回复。"
	if !chinese {
		footer = "This email was sent automatically by the ticket system. Please do not reply directly."
	}
	return fmt.Sprintf(`<!doctype html><html lang="%s"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>%s</title></head><body style="margin:0;padding:0;background:#f1f5f9;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','Microsoft YaHei',sans-serif;color:#0f172a"><table role="presentation" width="100%%" cellpadding="0" cellspacing="0" style="background:#f1f5f9"><tr><td align="center" style="padding:24px 12px"><table role="presentation" width="640" cellpadding="0" cellspacing="0" style="width:100%%;max-width:640px;table-layout:fixed;background:#ffffff;border:1px solid #e2e8f0;border-radius:16px"><tr><td style="padding:24px;background:#0f3d3a;border-radius:16px 16px 0 0;color:#ffffff"><p style="margin:0 0 16px;font-size:12px;letter-spacing:1px;color:#99f6e4">%s · SUPPORT</p><h1 style="margin:0 0 10px;font-size:22px;line-height:1.4;color:#ffffff">%s</h1><p style="margin:0;font-size:14px;line-height:1.7;color:#ccfbf1">%s</p></td></tr><tr><td style="padding:22px"><p style="margin:0 0 6px;font-size:12px;font-weight:700;color:#0f766e">TICKET #%d</p><h2 style="margin:0 0 14px;font-size:18px;line-height:1.5">%s</h2>%s%s<h3 style="margin:20px 0 14px;padding-top:16px;border-top:1px solid #e2e8f0;font-size:14px;color:#475569">%s</h3>%s</td></tr><tr><td style="padding:16px 22px;background:#f8fafc;border-top:1px solid #e2e8f0;border-radius:0 0 16px 16px"><p style="margin:0;font-size:12px;line-height:1.7;color:#64748b">%s</p></td></tr></table></td></tr></table></body></html>`,
		map[bool]string{true: "zh-CN", false: "en"}[chinese], html.EscapeString(in.Title),
		html.EscapeString(in.SiteName), html.EscapeString(in.Title), html.EscapeString(in.Intro),
		in.Ticket.ID, html.EscapeString(in.Ticket.Subject), requester, action, historyLabel, conversation.String(), footer)
}
