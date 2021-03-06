package bot

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/nlopes/slack"

	"github.com/rusenask/keel/approvals"
	"github.com/rusenask/keel/cache/memory"
	"github.com/rusenask/keel/constants"
	"github.com/rusenask/keel/extension/approval"
	"github.com/rusenask/keel/types"
	"github.com/rusenask/keel/util/codecs"

	"testing"

	testutil "github.com/rusenask/keel/util/testing"
)

type fakeProvider struct {
	submitted []types.Event
	images    []*types.TrackedImage
}

func (p *fakeProvider) Submit(event types.Event) error {
	p.submitted = append(p.submitted, event)
	return nil
}

func (p *fakeProvider) TrackedImages() ([]*types.TrackedImage, error) {
	return p.images, nil
}

func (p *fakeProvider) List() []string {
	return []string{"fakeprovider"}
}
func (p *fakeProvider) Stop() {
	return
}
func (p *fakeProvider) GetName() string {
	return "fp"
}

type postedMessage struct {
	channel string
	text    string
	params  slack.PostMessageParameters
}

type fakeSlackImplementer struct {
	postedMessages []postedMessage
}

func (i *fakeSlackImplementer) PostMessage(channel, text string, params slack.PostMessageParameters) (string, string, error) {
	i.postedMessages = append(i.postedMessages, postedMessage{
		channel: channel,
		text:    text,
		params:  params,
	})
	return "", "", nil
}

func TestBotRequest(t *testing.T) {
	f8s := &testutil.FakeK8sImplementer{}

	fi := &fakeSlackImplementer{}
	mem := memory.NewMemoryCache(100*time.Second, 100*time.Second, 10*time.Second)

	token := os.Getenv(constants.EnvSlackToken)
	if token == "" {
		t.Skip()
	}

	am := approvals.New(mem, codecs.DefaultSerializer())

	bot := New("keel", token, f8s, am)
	// replacing slack client so we can receive webhooks
	bot.slackHTTPClient = fi

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bot.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start bot: %s", err)
	}

	time.Sleep(1 * time.Second)

	err = am.Create(&types.Approval{
		Identifier:     "k8s/project/repo:1.2.3",
		VotesRequired:  1,
		CurrentVersion: "2.3.4",
		NewVersion:     "3.4.5",
		Event: &types.Event{
			Repository: types.Repository{
				Name: "project/repo",
				Tag:  "2.3.4",
			},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error while creating : %s", err)
	}

	time.Sleep(1 * time.Second)

	if len(fi.postedMessages) != 1 {
		t.Errorf("expected to find one message, but got: %d", len(fi.postedMessages))
	}
}

func TestProcessApprovedResponse(t *testing.T) {
	f8s := &testutil.FakeK8sImplementer{}
	fi := &fakeSlackImplementer{}
	mem := memory.NewMemoryCache(100*time.Second, 100*time.Second, 10*time.Second)

	token := os.Getenv(constants.EnvSlackToken)
	if token == "" {
		t.Skip()
	}

	am := approvals.New(mem, codecs.DefaultSerializer())

	bot := New("keel", token, f8s, am)
	// replacing slack client so we can receive webhooks
	bot.slackHTTPClient = fi

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := bot.Start(ctx)
	if err != nil {
		t.Fatalf("failed to start bot: %s", err)
	}

	time.Sleep(1 * time.Second)

	err = am.Create(&types.Approval{
		Identifier:     "k8s/project/repo:1.2.3",
		VotesRequired:  1,
		CurrentVersion: "2.3.4",
		NewVersion:     "3.4.5",
		Event: &types.Event{
			Repository: types.Repository{
				Name: "project/repo",
				Tag:  "2.3.4",
			},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error while creating : %s", err)
	}

	time.Sleep(1 * time.Second)

	if len(fi.postedMessages) != 1 {
		t.Errorf("expected to find one message")
	}
}

func TestProcessApprovalReply(t *testing.T) {
	f8s := &testutil.FakeK8sImplementer{}
	fi := &fakeSlackImplementer{}
	mem := memory.NewMemoryCache(100*time.Second, 100*time.Second, 10*time.Second)

	token := os.Getenv(constants.EnvSlackToken)
	if token == "" {
		t.Skip()
	}

	am := approvals.New(mem, codecs.DefaultSerializer())

	identifier := "k8s/project/repo:1.2.3"

	// creating initial approve request
	err := am.Create(&types.Approval{
		Identifier:     identifier,
		VotesRequired:  2,
		CurrentVersion: "2.3.4",
		NewVersion:     "3.4.5",
		Event: &types.Event{
			Repository: types.Repository{
				Name: "project/repo",
				Tag:  "2.3.4",
			},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error while creating : %s", err)
	}

	bot := New("keel", token, f8s, am)
	// replacing slack client so we can receive webhooks
	bot.slackHTTPClient = fi

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bot.ctx = ctx

	go bot.processApprovalResponses()

	time.Sleep(1 * time.Second)

	// approval resp
	bot.approvalsRespCh <- &approvalResponse{
		User:   "123",
		Status: types.ApprovalStatusApproved,
		Text:   fmt.Sprintf("%s %s", approvalResponseKeyword, identifier),
	}

	time.Sleep(1 * time.Second)

	updated, err := am.Get(identifier)
	if err != nil {
		t.Fatalf("failed to get approval, error: %s", err)
	}

	if updated.VotesReceived != 1 {
		t.Errorf("expected to find 1 received vote, found %d", updated.VotesReceived)
	}

	if updated.Status() != types.ApprovalStatusPending {
		t.Errorf("expected approval to be in status pending but got: %s", updated.Status())
	}

	if len(fi.postedMessages) != 1 {
		t.Errorf("expected to find one message")
	}

}

func TestProcessRejectedReply(t *testing.T) {
	f8s := &testutil.FakeK8sImplementer{}
	fi := &fakeSlackImplementer{}
	mem := memory.NewMemoryCache(100*time.Hour, 100*time.Hour, 100*time.Hour)

	// token := os.Getenv(constants.EnvSlackToken)
	// if token == "" {
	// 	t.Skip()
	// }

	identifier := "k8s/project/repo:1.2.3"

	am := approvals.New(mem, codecs.DefaultSerializer())
	// creating initial approve request
	err := am.Create(&types.Approval{
		Identifier:     identifier,
		VotesRequired:  2,
		CurrentVersion: "2.3.4",
		NewVersion:     "3.4.5",
		Event: &types.Event{
			Repository: types.Repository{
				Name: "project/repo",
				Tag:  "2.3.4",
			},
		},
	})

	if err != nil {
		t.Fatalf("unexpected error while creating : %s", err)
	}

	bot := New("keel", "random", f8s, am)

	collector := approval.New()
	collector.Configure(am)

	// replacing slack client so we can receive webhooks
	bot.slackHTTPClient = fi

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bot.ctx = ctx

	go bot.processApprovalResponses()

	time.Sleep(1 * time.Second)

	// approval resp
	bot.approvalsRespCh <- &approvalResponse{
		User:   "123",
		Status: types.ApprovalStatusRejected,
		Text:   fmt.Sprintf("%s %s", rejectResponseKeyword, identifier),
	}

	time.Sleep(1 * time.Second)

	updated, err := am.Get(identifier)
	if err != nil {
		t.Fatalf("failed to get approval, error: %s", err)
	}

	if updated.VotesReceived != 0 {
		t.Errorf("expected to find 0 received vote, found %d", updated.VotesReceived)
	}

	// if updated.Status() != types.ApprovalStatusRejected {
	if updated.Status() != types.ApprovalStatusRejected {
		t.Errorf("expected approval to be in status rejected but got: %s", updated.Status())
	}

	fmt.Println(updated.Status())

	if len(fi.postedMessages) != 1 {
		t.Errorf("expected to find one message")
	}

}
