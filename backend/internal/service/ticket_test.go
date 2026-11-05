package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

const ticketTestUUID = "6b7a9b04-1952-491c-ae6d-a2da990d8a8b"

type ticketUsersStub struct {
	UserRepository
	user *User
	err  error
}

func (s *ticketUsersStub) GetByID(context.Context, int64) (*User, error) { return s.user, s.err }

type ticketRepoStub struct {
	TicketRepository
	ticket           Ticket
	messages         []TicketMessage
	created          *CreateTicketInput
	createdCount     int
	hash             string
	listed           bool
	mutated          bool
	proactiveUserID  int64
	proactiveSubject string
	proactiveContent string
	outboxPending    int64
	outboxFailed     int64
	emailDetail      *TicketDetail
}

func (r *ticketRepoStub) Create(_ context.Context, _ TicketActor, in CreateTicketInput, hash string) (int64, error) {
	r.created, r.hash = &in, hash
	r.createdCount++
	r.ticket.ID = int64(10 + r.createdCount)
	return r.ticket.ID, nil
}

func (r *ticketRepoStub) CreateForUser(_ context.Context, _ TicketActor, userID int64, subject, content string) (int64, error) {
	r.proactiveUserID, r.proactiveSubject, r.proactiveContent = userID, subject, content
	return 10, nil
}

func (r *ticketRepoStub) Detail(context.Context, TicketActor, int64, int64) (*TicketDetail, error) {
	ticket := r.ticket
	return &TicketDetail{Ticket: &ticket, Messages: r.messages}, nil
}

func (r *ticketRepoStub) Mutate(_ context.Context, _ TicketActor, _ int64, _, hash string, fn func(*Ticket) (*TicketChange, error)) error {
	ticket := r.ticket
	plan, err := fn(&ticket)
	if err != nil {
		return err
	}
	r.ticket, r.messages, r.hash, r.mutated = *plan.Ticket, plan.Messages, hash, true
	return nil
}

func (r *ticketRepoStub) List(context.Context, TicketActor, TicketFilter) ([]Ticket, int64, error) {
	r.listed = true
	return []Ticket{r.ticket}, 1, nil
}

func (r *ticketRepoStub) Stats(context.Context, TicketActor) (*TicketStats, error) {
	return &TicketStats{Total: 1, Open: 1}, nil
}

func (r *ticketRepoStub) EmailOutboxStats(context.Context) (int64, int64, error) {
	return r.outboxPending, r.outboxFailed, nil
}

func supportTicketTestService(admin bool) (*TicketService, *ticketRepoStub, TicketActor) {
	role := RoleUser
	if admin {
		role = RoleAdmin
	}
	repo := &ticketRepoStub{ticket: Ticket{ID: 10, UserID: 7, Status: "open", Priority: "normal"}}
	svc := NewTicketService(repo, &ticketUsersStub{user: &User{ID: 7, Status: StatusActive, Role: role}})
	return svc, repo, TicketActor{UserID: 7, Admin: admin}
}

type ticketSettingRepoStub struct {
	SettingRepository
	values  map[string]string
	deleted []string
	err     error
}

func (s *ticketSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", ErrSettingNotFound
}

func (s *ticketSettingRepoStub) Set(_ context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

func (s *ticketSettingRepoStub) Delete(_ context.Context, key string) error {
	delete(s.values, key)
	s.deleted = append(s.deleted, key)
	return nil
}

func TestCreateForUserRequiresAdminAndDefaultsSubject(t *testing.T) {
	svc, repo, actor := supportTicketTestService(true)
	detail, err := svc.CreateForUser(context.Background(), actor, AdminCreateTicketInput{UserID: 7, Content: "我们检测到你的请求异常，请联系客服"})
	require.NoError(t, err)
	require.Equal(t, int64(10), detail.Ticket.ID)
	require.Equal(t, int64(7), repo.proactiveUserID)
	require.Equal(t, "客服消息", repo.proactiveSubject)
	require.Equal(t, "我们检测到你的请求异常，请联系客服", repo.proactiveContent)

	_, err = svc.CreateForUser(context.Background(), TicketActor{UserID: 7}, AdminCreateTicketInput{UserID: 7, Content: "hello"})
	require.Equal(t, 403, infraerrors.Code(err))
}

func TestTicketCreateValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*CreateTicketInput)
		valid  bool
	}{
		{"valid", func(*CreateTicketInput) {}, true},
		{"unicode boundaries", func(in *CreateTicketInput) {
			in.Subject = strings.Repeat("界", 160)
			in.Content = strings.Repeat("界", 10000)
			in.Contact = strings.Repeat("界", 200)
		}, true},
		{"blank subject", func(in *CreateTicketInput) { in.Subject = "  \n" }, false},
		{"long subject", func(in *CreateTicketInput) { in.Subject = strings.Repeat("界", 161) }, false},
		{"blank content", func(in *CreateTicketInput) { in.Content = " \n\t" }, false},
		{"long content", func(in *CreateTicketInput) { in.Content = strings.Repeat("界", 10001) }, false},
		{"long contact", func(in *CreateTicketInput) { in.Contact = strings.Repeat("界", 201) }, false},
		{"nul contact", func(in *CreateTicketInput) { in.Contact = "a\x00b" }, false},
		{"invalid utf8", func(in *CreateTicketInput) { in.Content = string([]byte{0xff}) }, false},
		{"invalid category", func(in *CreateTicketInput) { in.Category = "unknown" }, false},
		{"invalid priority", func(in *CreateTicketInput) { in.Priority = "low" }, false},
		{"missing UUID", func(in *CreateTicketInput) { in.ClientID = "" }, false},
		{"nil UUID", func(in *CreateTicketInput) { in.ClientID = "00000000-0000-0000-0000-000000000000" }, false},
		{"compact UUID", func(in *CreateTicketInput) { in.ClientID = strings.ReplaceAll(ticketTestUUID, "-", "") }, false},
		{"uppercase UUID", func(in *CreateTicketInput) { in.ClientID = strings.ToUpper(ticketTestUUID) }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, actor := supportTicketTestService(false)
			in := CreateTicketInput{Subject: "help", Content: "request", Category: "api", Priority: "normal", ClientID: ticketTestUUID}
			tc.change(&in)
			_, err := svc.Create(context.Background(), actor, in)
			if !tc.valid {
				require.Equal(t, 400, infraerrors.Code(err))
				require.Nil(t, repo.created)
				return
			}
			require.NoError(t, err)
			require.Equal(t, ticketTestUUID, repo.created.ClientID)
			require.Len(t, repo.hash, 64)
		})
	}
}

func TestTicketServiceRejectsAnonymousAndNonAdmin(t *testing.T) {
	for _, action := range []struct {
		name string
		run  func(*TicketService, TicketActor) error
	}{
		{"list", func(s *TicketService, a TicketActor) error {
			_, _, err := s.List(context.Background(), a, TicketFilter{})
			return err
		}},
		{"stats", func(s *TicketService, a TicketActor) error { _, err := s.Stats(context.Background(), a); return err }},
		{"detail", func(s *TicketService, a TicketActor) error {
			_, err := s.Detail(context.Background(), a, 10, 0)
			return err
		}},
		{"create", func(s *TicketService, a TicketActor) error {
			_, err := s.Create(context.Background(), a, CreateTicketInput{})
			return err
		}},
		{"reply", func(s *TicketService, a TicketActor) error {
			_, err := s.Reply(context.Background(), a, 10, ReplyTicketInput{})
			return err
		}},
		{"update", func(s *TicketService, a TicketActor) error {
			_, err := s.Update(context.Background(), a, 10, UpdateTicketInput{})
			return err
		}},
		{"read", func(s *TicketService, a TicketActor) error { return s.MarkRead(context.Background(), a, 10, 2) }},
		{"settings", func(s *TicketService, a TicketActor) error {
			_, err := s.NotificationSettings(context.Background(), a)
			return err
		}},
	} {
		t.Run(action.name, func(t *testing.T) {
			svc, _, _ := supportTicketTestService(false)
			require.Equal(t, 401, infraerrors.Code(action.run(svc, TicketActor{})))
			require.Equal(t, 403, infraerrors.Code(action.run(svc, TicketActor{UserID: 7, Admin: true})))
			svc.users = &ticketUsersStub{user: &User{ID: 7, Status: "disabled", Role: RoleAdmin}}
			require.Equal(t, 401, infraerrors.Code(action.run(svc, TicketActor{UserID: 7, Admin: true})))
		})
	}
}

func TestTicketReplyStateMachine(t *testing.T) {
	for _, admin := range []bool{false, true} {
		for _, status := range []string{"open", "in_progress", "waiting_user", "resolved", "closed"} {
			t.Run(TicketActor{Admin: admin}.Role()+"/"+status, func(t *testing.T) {
				svc, repo, actor := supportTicketTestService(admin)
				repo.ticket.Status = status
				detail, err := svc.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID})
				if status == "closed" {
					require.ErrorIs(t, err, ErrTicketClosed)
					require.False(t, repo.mutated)
					return
				}
				require.NoError(t, err)
				expected := "open"
				if admin {
					expected = "waiting_user"
				}
				require.Equal(t, expected, detail.Ticket.Status)
				require.Equal(t, "reply", detail.Messages[len(detail.Messages)-1].Kind)
				if status != expected {
					require.Equal(t, "status_changed", detail.Messages[0].EventType)
					require.Equal(t, status, detail.Messages[0].EventData["from"])
				}
			})
		}
	}
}

func TestTicketReplyPermissionsAndDefaults(t *testing.T) {
	svc, repo, actor := supportTicketTestService(false)
	_, err := svc.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID, Status: ticketString("resolved")})
	require.Equal(t, 400, infraerrors.Code(err))
	require.False(t, repo.mutated)
	repo.ticket.UserID = 8
	_, err = svc.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID})
	require.ErrorIs(t, err, ErrTicketNotFound)
	svc, repo, actor = supportTicketTestService(true)
	for _, status := range []string{"waiting_user", "in_progress", "resolved", "open", "closed", "bad"} {
		_, err = svc.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID, Status: &status})
		if status == "open" || status == "closed" || status == "bad" {
			require.Equal(t, 400, infraerrors.Code(err))
		} else {
			require.NoError(t, err)
			require.Equal(t, status, repo.ticket.Status)
		}
	}
	_, err = svc.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID})
	require.NoError(t, err)
	defaultHash := repo.hash
	_, err = svc.Reply(context.Background(), actor, 10, ReplyTicketInput{Content: "reply", ClientID: ticketTestUUID, Status: ticketString("waiting_user")})
	require.NoError(t, err)
	require.Equal(t, defaultHash, repo.hash)
}

func TestTicketUpdatePermissionsAndEvents(t *testing.T) {
	for _, in := range []UpdateTicketInput{{}, {Status: ticketString("resolved")}, {Priority: ticketString("urgent")}, {AssignedTo: ticketString("me")}} {
		svc, repo, actor := supportTicketTestService(false)
		_, err := svc.Update(context.Background(), actor, 10, in)
		require.Equal(t, 400, infraerrors.Code(err))
		require.False(t, repo.mutated)
	}
	svc, repo, actor := supportTicketTestService(false)
	_, err := svc.Update(context.Background(), actor, 10, UpdateTicketInput{Status: ticketString("closed")})
	require.NoError(t, err)
	require.Equal(t, "closed", repo.ticket.Status)
	_, err = svc.Update(context.Background(), actor, 10, UpdateTicketInput{Status: ticketString("open")})
	require.NoError(t, err)
	require.Equal(t, "closed", repo.messages[0].EventData["from"])
	repo.ticket.UserID = 8
	_, err = svc.Update(context.Background(), actor, 10, UpdateTicketInput{Status: ticketString("closed")})
	require.ErrorIs(t, err, ErrTicketNotFound)
	svc, repo, actor = supportTicketTestService(true)
	_, err = svc.Update(context.Background(), actor, 10, UpdateTicketInput{Status: ticketString("closed"), Priority: ticketString("urgent"), AssignedTo: ticketString("me")})
	require.NoError(t, err)
	require.Equal(t, "closed", repo.ticket.Status)
	require.Equal(t, int64(7), *repo.ticket.AssigneeID)
	require.Len(t, repo.messages, 3)
	for _, message := range repo.messages {
		require.Equal(t, "event", message.Kind)
		require.NotEmpty(t, message.Content)
		require.Equal(t, "admin", message.AuthorRole)
	}
	_, err = svc.Update(context.Background(), actor, 10, UpdateTicketInput{AssignedTo: ticketString("unassigned")})
	require.NoError(t, err)
	require.Nil(t, repo.ticket.AssigneeID)
	_, err = svc.Update(context.Background(), actor, 10, UpdateTicketInput{AssignedTo: ticketString("123")})
	require.Equal(t, 400, infraerrors.Code(err))
}

func TestTicketFiltersAndCursorValidation(t *testing.T) {
	for _, admin := range []bool{false, true} {
		svc, repo, actor := supportTicketTestService(admin)
		_, _, err := svc.List(context.Background(), actor, TicketFilter{PaginationParams: pagination.DefaultPagination(), Status: "active"})
		require.NoError(t, err)
		require.True(t, repo.listed)
		_, err = svc.Update(context.Background(), actor, 10, UpdateTicketInput{Status: ticketString("active")})
		require.Equal(t, 400, infraerrors.Code(err))
	}
	for _, change := range []func(*TicketFilter){
		func(f *TicketFilter) { f.Page = 0 }, func(f *TicketFilter) { f.PageSize = 101 }, func(f *TicketFilter) { f.Page = int(^uint(0) >> 1) },
		func(f *TicketFilter) { f.Status = "bad" }, func(f *TicketFilter) { f.Priority = "bad" }, func(f *TicketFilter) { f.Category = "bad" },
		func(f *TicketFilter) { f.AssignedTo = "mine" }, func(f *TicketFilter) { f.Search = strings.Repeat("界", 161) },
		func(f *TicketFilter) { f.Search = string([]byte{0xff}) },
	} {
		svc, repo, actor := supportTicketTestService(false)
		filter := TicketFilter{PaginationParams: pagination.DefaultPagination()}
		change(&filter)
		_, _, err := svc.List(context.Background(), actor, filter)
		require.Equal(t, 400, infraerrors.Code(err))
		require.False(t, repo.listed)
	}
	svc, _, actor := supportTicketTestService(false)
	_, err := svc.Detail(context.Background(), actor, 10, -1)
	require.Equal(t, 400, infraerrors.Code(err))
	require.ErrorIs(t, svc.MarkRead(context.Background(), actor, 10, 0), ErrTicketReadCursor)
	svc.users = &ticketUsersStub{err: errors.New("lookup failed")}
	_, err = svc.Detail(context.Background(), actor, 10, 0)
	require.EqualError(t, err, "lookup failed")
}

// This deployment has no one-ticket-per-day policy. The service must accept
// consecutive conversations; the database test asserts the same end to end.
func TestTicketAllowsMultipleConversationsWithoutDailyQuota(t *testing.T) {
	svc, repo, actor := supportTicketTestService(false)
	first, err := svc.Create(context.Background(), actor, CreateTicketInput{
		Subject: "first", Content: "one", Category: "api", Priority: "normal", ClientID: ticketTestUUID,
	})
	require.NoError(t, err)
	second, err := svc.Create(context.Background(), actor, CreateTicketInput{
		Subject: "second", Content: "two", Category: "billing", Priority: "high", ClientID: "0f4e5f26-6d1c-4b7f-9a2e-3b2a1c9d8e7f",
	})
	require.NoError(t, err)
	require.NotEqual(t, first.Ticket.ID, second.Ticket.ID)
	require.Equal(t, 2, repo.createdCount)
}

func TestTicketNotificationSettings(t *testing.T) {
	svc, repo, actor := supportTicketTestService(true)
	settings := &ticketSettingRepoStub{values: map[string]string{}}
	svc.settings = settings
	repo.outboxPending, repo.outboxFailed = 3, 1
	current, err := svc.NotificationSettings(context.Background(), actor)
	require.NoError(t, err)
	require.True(t, current.Enabled)
	require.Empty(t, current.AdminEmail)
	require.Equal(t, int64(3), current.PendingEmail)
	require.Equal(t, int64(1), current.FailedEmail)

	disabled := false
	address := "support@example.test"
	updated, err := svc.UpdateNotificationSettings(context.Background(), actor, TicketNotificationSettingsInput{Enabled: &disabled, AdminEmail: &address})
	require.NoError(t, err)
	require.False(t, updated.Enabled)
	require.Equal(t, address, updated.AdminEmail)
	require.Equal(t, "false", settings.values[SettingKeyTicketEmailEnabled])

	invalid := "not-an-email"
	_, err = svc.UpdateNotificationSettings(context.Background(), actor, TicketNotificationSettingsInput{AdminEmail: &invalid})
	require.Equal(t, 400, infraerrors.Code(err))

	empty := ""
	_, err = svc.UpdateNotificationSettings(context.Background(), actor, TicketNotificationSettingsInput{AdminEmail: &empty})
	require.NoError(t, err)
	require.Contains(t, settings.deleted, SettingKeyTicketAdminEmail)

	_, err = svc.UpdateNotificationSettings(context.Background(), actor, TicketNotificationSettingsInput{})
	require.Equal(t, 400, infraerrors.Code(err))
}

func TestTicketEmailTemplateEscapesAndScopesConversation(t *testing.T) {
	messages := []TicketMessage{
		{ID: 1, AuthorRole: "user", Kind: "reply", AuthorName: "张三", Content: "<img src=x onerror=alert(1)>\nsecond line", CreatedAt: ticketTestTime()},
		{ID: 2, AuthorRole: "admin", Kind: "reply", AuthorName: "客服", Content: "SECRET-LATER", CreatedAt: ticketTestTime()},
	}
	userHTML := ticketEmailHTML(ticketEmailRenderInput{
		Locale: notificationEmailLocaleChinese, SiteName: "站点", Title: "工单回复", Intro: "说明",
		Ticket: &Ticket{ID: 9, Subject: `<b>主题</b>`}, Messages: messages, ThroughMessageID: 1,
		Link: "https://panel.example.test/tickets/9",
	})
	require.NotContains(t, userHTML, "<img")
	require.NotContains(t, userHTML, "<b>主题</b>")
	require.Contains(t, userHTML, "&lt;img")
	require.Contains(t, userHTML, "&lt;b&gt;主题&lt;/b&gt;")
	require.Contains(t, userHTML, "最新回复")
	require.NotContains(t, userHTML, "SECRET-LATER")
	require.Contains(t, userHTML, "https://panel.example.test/tickets/9")

	adminHTML := ticketEmailHTML(ticketEmailRenderInput{
		Locale: notificationEmailLocaleChinese, SiteName: "站点", Title: "新消息", Intro: "说明",
		Ticket: &Ticket{ID: 9, Subject: "主题"}, Messages: messages, ThroughMessageID: 1,
		AdminView: true, RequesterName: "张三", RequesterEmail: "zhang@example.test",
	})
	require.Contains(t, adminHTML, "zhang@example.test")
	require.Contains(t, adminHTML, "用户")
	require.NotContains(t, adminHTML, "SECRET-LATER")
}

func TestTicketEmailLinkRejectsUnsafeOrigins(t *testing.T) {
	settings := &ticketSettingRepoStub{values: map[string]string{
		SettingKeyFrontendURL: "javascript:alert(1)//evil.test",
		SettingKeyAPIBaseURL:  "https://panel.example.test/base/",
	}}
	svc := &TicketService{settings: settings}
	require.Equal(t, "https://panel.example.test/base/tickets/5", svc.emailLink(context.Background(), "/tickets/5"))

	settings.values[SettingKeyFrontendURL] = "https://user:pass@evil.test"
	settings.values[SettingKeyAPIBaseURL] = ""
	require.Empty(t, svc.emailLink(context.Background(), "/tickets/5"))
}

func TestTicketEmailPermanentClassification(t *testing.T) {
	require.True(t, IsTicketEmailPermanent(ticketEmailPermanent(errors.New("no recipient"))))
	require.False(t, IsTicketEmailPermanent(errors.New("smtp timeout")))
}

func ticketString(value string) *string { return &value }

func ticketTestTime() time.Time {
	return time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
}
