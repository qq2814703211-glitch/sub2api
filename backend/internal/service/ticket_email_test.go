package service

import (
	"bufio"
	"context"
	"errors"
	"mime"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ticketFakeSMTP accepts the minimal AUTH PLAIN dialogue used by EmailService and
// captures the raw DATA of every message, so ticket notification delivery is
// exercised through the real SMTP client rather than a mock.
type ticketFakeSMTP struct {
	listener net.Listener
	messages chan string
	wg       sync.WaitGroup
}

func startTicketFakeSMTP(t *testing.T) *ticketFakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := &ticketFakeSMTP{listener: listener, messages: make(chan string, 8)}
	server.wg.Add(1)
	go func() {
		defer server.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			server.wg.Add(1)
			go func() {
				defer server.wg.Done()
				server.serveTicket(conn)
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		server.wg.Wait()
		close(server.messages)
	})
	return server
}

func (s *ticketFakeSMTP) serveTicket(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	_, _ = conn.Write([]byte("220 test ESMTP\r\n"))
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			_, _ = conn.Write([]byte("250-test\r\n250-AUTH PLAIN\r\n250 OK\r\n"))
		case strings.HasPrefix(command, "AUTH"):
			_, _ = conn.Write([]byte("235 2.7.0 authentication successful\r\n"))
		case strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
			_, _ = conn.Write([]byte("250 OK\r\n"))
		case strings.HasPrefix(command, "DATA"):
			_, _ = conn.Write([]byte("354 end with <CRLF>.<CRLF>\r\n"))
			var data []byte
			for {
				payload, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if payload == ".\r\n" {
					break
				}
				data = append(data, payload...)
			}
			s.messages <- string(data)
			_, _ = conn.Write([]byte("250 queued\r\n"))
		case strings.HasPrefix(command, "QUIT"):
			_, _ = conn.Write([]byte("221 bye\r\n"))
			return
		default:
			_, _ = conn.Write([]byte("250 OK\r\n"))
		}
	}
}

func (s *ticketFakeSMTP) next(t *testing.T) string {
	t.Helper()
	select {
	case message := <-s.messages:
		return message
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for SMTP message")
		return ""
	}
}

func (s *ticketSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *ticketRepoStub) EmailConversation(_ context.Context, id, through int64) (*TicketDetail, error) {
	if r.emailDetail == nil || r.emailDetail.Ticket.ID != id {
		return nil, ErrTicketNotFound
	}
	for _, message := range r.emailDetail.Messages {
		if message.ID == through && message.Kind == "reply" {
			return r.emailDetail, nil
		}
	}
	return nil, ErrTicketNotFound
}

func ticketEmailTestService(t *testing.T, adminEmail string) (*TicketService, *ticketFakeSMTP, *ticketRepoStub) {
	t.Helper()
	smtp := startTicketFakeSMTP(t)
	_, port, err := net.SplitHostPort(smtp.listener.Addr().String())
	require.NoError(t, err)
	settings := &ticketSettingRepoStub{values: map[string]string{
		SettingKeySMTPHost:         "127.0.0.1",
		SettingKeySMTPPort:         port,
		SettingKeySMTPUsername:     "mailer",
		SettingKeySMTPPassword:     "secret",
		SettingKeySMTPFrom:         "support@example.test",
		SettingKeySMTPUseTLS:       "false",
		SettingKeySiteName:         "TicketSite",
		SettingKeyTicketAdminEmail: adminEmail,
	}}
	email := NewEmailService(settings, nil)
	repo := &ticketRepoStub{
		emailDetail: &TicketDetail{
			Ticket: &Ticket{ID: 5, UserID: 9, Subject: "API failure", Category: "api", Status: "open"},
			Messages: []TicketMessage{
				{ID: 1, TicketID: 5, AuthorRole: "user", AuthorName: "Alice", Kind: "reply", Content: "my request failed", CreatedAt: time.Now()},
				{ID: 2, TicketID: 5, AuthorRole: "admin", AuthorName: "Support", Kind: "reply", Content: "please retry <script>alert(1)</script>", CreatedAt: time.Now()},
			},
		},
	}
	users := &ticketUsersStub{user: &User{ID: 9, Email: "alice@example.test", Username: "Alice", Status: StatusActive, Role: RoleUser}}
	svc := NewTicketService(repo, users)
	svc.email = email
	svc.settings = settings
	return svc, smtp, repo
}

func TestTicketEmailDeliversAdminReplyToOwner(t *testing.T) {
	svc, smtp, _ := ticketEmailTestService(t, "")
	err := svc.sendTicketEmail(context.Background(), TicketEmailJob{TicketID: 5, MessageID: 2, Audience: "user"})
	require.NoError(t, err)
	raw := smtp.next(t)
	require.Contains(t, raw, "To: <alice@example.test>")
	require.Contains(t, raw, "&lt;script&gt;alert(1)&lt;/script&gt;")
	require.NotContains(t, raw, "<script>alert(1)</script>")
	subject, err := new(mime.WordDecoder).DecodeHeader(between(raw, "Subject: ", "\r\n"))
	require.NoError(t, err)
	require.Contains(t, subject, "TicketSite")
	require.Contains(t, subject, "5")
}

func TestTicketEmailRoutesUserMessageToSupportInbox(t *testing.T) {
	svc, smtp, repo := ticketEmailTestService(t, "inbox@example.test")
	repo.emailDetail.Messages[1] = TicketMessage{ID: 3, TicketID: 5, AuthorRole: "user", AuthorName: "Alice", Kind: "reply", Content: "any update?", CreatedAt: time.Now()}
	err := svc.sendTicketEmail(context.Background(), TicketEmailJob{TicketID: 5, MessageID: 3, Audience: "admin"})
	require.NoError(t, err)
	raw := smtp.next(t)
	require.Contains(t, raw, "To: <inbox@example.test>")
	require.Contains(t, raw, "alice@example.tes")
}

func TestTicketEmailInvalidOwnerIsPermanent(t *testing.T) {
	svc, smtp, _ := ticketEmailTestService(t, "")
	svc.users = &ticketUsersStub{user: &User{ID: 9, Email: "not-an-email", Username: "Alice", Status: StatusActive, Role: RoleUser}}
	err := svc.sendTicketEmail(context.Background(), TicketEmailJob{TicketID: 5, MessageID: 2, Audience: "user"})
	require.Error(t, err)
	require.True(t, IsTicketEmailPermanent(err))
	select {
	case message := <-smtp.messages:
		t.Fatalf("unexpected delivery: %s", message)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTicketEmailDisabledSettingDefaultsAndToggle(t *testing.T) {
	settings := &ticketSettingRepoStub{values: map[string]string{}}
	svc := &TicketService{settings: settings}
	require.True(t, svc.emailNotificationsEnabled(context.Background()))
	settings.values[SettingKeyTicketEmailEnabled] = "false"
	require.False(t, svc.emailNotificationsEnabled(context.Background()))
	settings.values[SettingKeyTicketEmailEnabled] = "true"
	require.True(t, svc.emailNotificationsEnabled(context.Background()))
	settings.err = errors.New("db unavailable")
	require.False(t, svc.emailNotificationsEnabled(context.Background()))
}

func between(value, start, end string) string {
	left := strings.Index(value, start)
	if left < 0 {
		return ""
	}
	left += len(start)
	right := strings.Index(value[left:], end)
	if right < 0 {
		return value[left:]
	}
	return value[left : left+right]
}
