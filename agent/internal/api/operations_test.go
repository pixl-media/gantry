package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/compose"
	"github.com/pixl-media/gantry/agent/internal/operations"
	"github.com/pixl-media/gantry/agent/internal/stacks"
)

// runner is a fake Compose runner. If block is set, `up` waits until it is
// closed or the operation is canceled.
type runner struct {
	block chan struct{}
}

func (r runner) Run(ctx context.Context, _ compose.Project, args []string, out io.Writer) (int, error) {
	fmt.Fprintf(out, "ran %s\n", strings.Join(args, " "))
	if r.block != nil && args[0] == "up" {
		select {
		case <-r.block:
		case <-ctx.Done():
			return 130, ctx.Err()
		}
	}
	return 0, nil
}

func newOpsServer(t *testing.T, r runner) (http.Handler, *operations.Manager) {
	t.Helper()
	ops := operations.NewManager(context.Background(), r, operations.Options{})
	st := fakeStacks{
		unmanaged: []string{"ext"},
		plans: map[string]stacks.ServicePlan{
			"solo": {
				Service:  "solo",
				Affected: []stacks.Affected{{Service: "solo", Effect: stacks.EffectRestart, Reason: stacks.ReasonTarget}},
				Steps:    [][]string{{"stop", "solo"}, {"start", "solo"}},
			},
			"db": {
				Service: "db",
				Affected: []stacks.Affected{
					{Service: "db", Effect: stacks.EffectRestart, Reason: stacks.ReasonTarget},
					{Service: "app", Effect: stacks.EffectRestart, Reason: stacks.ReasonDependent},
				},
				Steps: [][]string{{"stop", "db", "app"}, {"start", "db", "app"}},
			},
			"blocker": {
				Service:  "blocker",
				Affected: []stacks.Affected{{Service: "blocker", Effect: stacks.EffectStart, Reason: stacks.ReasonTarget}},
				Steps:    [][]string{{"up", "-d", "--no-deps", "blocker"}},
			},
		},
		list:   stacks.List{DockerAvailable: true, Stacks: []stacks.Summary{{Name: "app", Managed: true, Status: stacks.StatusStopped}}},
		detail: stacks.Detail{Summary: stacks.Summary{Name: "app", Managed: true}},
	}
	return New(Config{Token: "t", UIToken: testUIToken, Stacks: st, Operations: ops}), ops
}

func request(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer t")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body)
	}
	return v
}

// waitDone polls an operation until it has finished.
func waitDone(t *testing.T, ops *operations.Manager, id string) operations.Snapshot {
	t.Helper()
	op, err := ops.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for op.Snapshot().Status == operations.StatusRunning {
		if time.Now().After(deadline) {
			t.Fatalf("operation %s didn't finish", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return op.Snapshot()
}

func TestRunStackAction(t *testing.T) {
	h, ops := newOpsServer(t, runner{})

	rec := request(t, h, http.MethodPost, "/api/v1/stacks/app/actions/start")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	op := decode[Operation](t, rec)
	if op.Action != OperationActionStart || op.Stack != "app" || !strings.HasPrefix(op.Id, "op_") {
		t.Errorf("operation = %+v", op)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/operations/"+op.Id {
		t.Errorf("Location = %q", loc)
	}
	if s := waitDone(t, ops, op.Id); s.Status != operations.StatusSucceeded {
		t.Errorf("final = %+v", s)
	}

	detail := decode[OperationDetail](t, request(t, h, http.MethodGet, "/api/v1/operations/"+op.Id))
	if detail.Status != OperationStatusSucceeded || !strings.Contains(detail.Output, "ran up -d --remove-orphans") {
		t.Errorf("detail = %+v", detail)
	}
}

func TestRunStackActionErrors(t *testing.T) {
	h, _ := newOpsServer(t, runner{})
	tests := []struct {
		path string
		want int
		code ErrorCode
	}{
		{"/api/v1/stacks/app/actions/explode", http.StatusBadRequest, ErrorCodeInvalidAction},
		{"/api/v1/stacks/Bad.Name/actions/start", http.StatusBadRequest, ErrorCodeInvalidName},
		{"/api/v1/stacks/nope/actions/start", http.StatusNotFound, ErrorCodeNotFound},
		{"/api/v1/stacks/ext/actions/stop", http.StatusConflict, ErrorCodeStackUnmanaged},
	}
	for _, tt := range tests {
		rec := request(t, h, http.MethodPost, tt.path)
		e := decode[Error](t, rec)
		if rec.Code != tt.want || e.Code == nil || *e.Code != tt.code {
			t.Errorf("POST %s = %d %+v, want %d %s", tt.path, rec.Code, e, tt.want, tt.code)
		}
	}
}

func TestBusyAndPreemption(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	h, ops := newOpsServer(t, runner{block: block})

	start := decode[Operation](t, request(t, h, http.MethodPost, "/api/v1/stacks/app/actions/start"))

	// While start runs: restart is rejected and names the running operation.
	rec := request(t, h, http.MethodPost, "/api/v1/stacks/app/actions/restart")
	e := decode[Error](t, rec)
	if rec.Code != http.StatusConflict || *e.Code != ErrorCodeStackBusy || e.Operation == nil || *e.Operation != start.Id {
		t.Errorf("restart while busy = %d %+v", rec.Code, e)
	}

	// The stack shows its active operation.
	list := decode[StackList](t, request(t, h, http.MethodGet, "/api/v1/stacks"))
	if a := list.Stacks[0].ActiveOperation; a == nil || a.Id != start.Id || a.Action != OperationActionStart {
		t.Errorf("activeOperation in list = %+v", a)
	}
	detail := decode[StackDetail](t, request(t, h, http.MethodGet, "/api/v1/stacks/app"))
	if a := detail.ActiveOperation; a == nil || a.Id != start.Id {
		t.Errorf("activeOperation in detail = %+v", a)
	}

	// stop takes precedence and cancels start.
	rec = request(t, h, http.MethodPost, "/api/v1/stacks/app/actions/stop")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("stop while busy = %d: %s", rec.Code, rec.Body)
	}
	stop := decode[Operation](t, rec)
	if s := waitDone(t, ops, start.Id); s.Status != operations.StatusCanceled || s.CanceledBy != stop.Id {
		t.Errorf("start = %+v, want canceled by %s", s, stop.Id)
	}
	if s := waitDone(t, ops, stop.Id); s.Status != operations.StatusSucceeded {
		t.Errorf("stop = %+v", s)
	}
}

func TestCancelOperation(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	h, ops := newOpsServer(t, runner{block: block})
	op := decode[Operation](t, request(t, h, http.MethodPost, "/api/v1/stacks/app/actions/start"))

	if rec := request(t, h, http.MethodPost, "/api/v1/operations/"+op.Id+"/cancel"); rec.Code != http.StatusAccepted {
		t.Fatalf("cancel = %d: %s", rec.Code, rec.Body)
	}
	if s := waitDone(t, ops, op.Id); s.Status != operations.StatusCanceled {
		t.Errorf("final = %+v", s)
	}
	rec := request(t, h, http.MethodPost, "/api/v1/operations/"+op.Id+"/cancel")
	if e := decode[Error](t, rec); rec.Code != http.StatusConflict || *e.Code != ErrorCodeOperationFinished {
		t.Errorf("cancel finished = %d %+v", rec.Code, e)
	}
	if rec := request(t, h, http.MethodPost, "/api/v1/operations/op_nope/cancel"); rec.Code != http.StatusNotFound {
		t.Errorf("cancel unknown = %d", rec.Code)
	}
}

func TestListOperations(t *testing.T) {
	h, ops := newOpsServer(t, runner{})
	a := decode[Operation](t, request(t, h, http.MethodPost, "/api/v1/stacks/app/actions/start"))
	waitDone(t, ops, a.Id)
	b := decode[Operation](t, request(t, h, http.MethodPost, "/api/v1/stacks/app/actions/stop"))
	waitDone(t, ops, b.Id)

	list := decode[OperationList](t, request(t, h, http.MethodGet, "/api/v1/operations?stack=app"))
	if len(list.Operations) != 2 || list.Operations[0].Id != b.Id || list.Operations[1].Id != a.Id {
		t.Errorf("list = %+v, want newest first", list)
	}
	if l := decode[OperationList](t, request(t, h, http.MethodGet, "/api/v1/operations?stack=other")); len(l.Operations) != 0 {
		t.Errorf("filtered = %+v", l)
	}
	if rec := request(t, h, http.MethodGet, "/api/v1/operations/op_nope"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown = %d", rec.Code)
	}
}

func TestOperationEvents(t *testing.T) {
	block := make(chan struct{})
	h, _ := newOpsServer(t, runner{block: block})
	srv := httptest.NewServer(h)
	defer srv.Close()

	op := decode[Operation](t, request(t, h, http.MethodPost, "/api/v1/stacks/app/actions/start"))

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/operations/"+op.Id+"/events", nil)
	req.Header.Set("Authorization", "Bearer t")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}

	// The stream is live: the backlog arrives while the operation still runs.
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	if first := string(buf[:n]); !strings.Contains(first, "event: output\ndata: $ docker compose up -d --remove-orphans\n\n") {
		t.Errorf("first chunk = %q", first)
	}

	close(block)
	rest, _ := io.ReadAll(resp.Body)
	all := string(buf[:n]) + string(rest)
	if !strings.Contains(all, "data: ran up -d --remove-orphans") {
		t.Errorf("stream = %q", all)
	}
	i := strings.Index(all, "event: done\ndata: ")
	if i < 0 {
		t.Fatalf("no done event in %q", all)
	}
	var final Operation
	if err := json.Unmarshal([]byte(strings.TrimSpace(all[i+len("event: done\ndata: "):])), &final); err != nil {
		t.Fatal(err)
	}
	if final.Status != OperationStatusSucceeded || final.Id != op.Id {
		t.Errorf("final = %+v", final)
	}
}

func TestOperationEndpointsNeedToken(t *testing.T) {
	h, _ := newOpsServer(t, runner{})
	for _, r := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/stacks/app/actions/start"},
		{http.MethodGet, "/api/v1/operations"},
		{http.MethodGet, "/api/v1/operations/op_x/events"},
		{http.MethodPost, "/api/v1/operations/op_x/cancel"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newRequest(r.method, r.path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without token = %d", r.method, r.path, rec.Code)
		}
	}
}

func requestJSON(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRunServiceAction(t *testing.T) {
	h, ops := newOpsServer(t, runner{})

	rec := request(t, h, http.MethodPost, "/api/v1/stacks/app/services/solo/actions/restart")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	op := decode[Operation](t, rec)
	if op.Action != OperationActionRestart || op.Service == nil || *op.Service != "solo" ||
		op.Services == nil || strings.Join(*op.Services, ",") != "solo" {
		t.Errorf("operation = %+v", op)
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/operations/"+op.Id {
		t.Errorf("Location = %q", loc)
	}
	waitDone(t, ops, op.Id)
	detail := decode[OperationDetail](t, request(t, h, http.MethodGet, "/api/v1/operations/"+op.Id))
	if !strings.Contains(detail.Output, "ran stop solo\n") || !strings.Contains(detail.Output, "ran start solo\n") ||
		detail.Service == nil || *detail.Service != "solo" {
		t.Errorf("detail = %+v", detail)
	}
}

func TestServiceActionConfirmation(t *testing.T) {
	h, ops := newOpsServer(t, runner{})
	const path = "/api/v1/stacks/app/services/db/actions/restart"

	// Without confirmation, the answer is the plan.
	for _, body := range []string{"", `{}`, `{"confirm":["db"]}`, `{"confirm":["db","app","web"]}`, `{"confirm":["db","db"]}`} {
		var rec *httptest.ResponseRecorder
		if body == "" {
			rec = request(t, h, http.MethodPost, path)
		} else {
			rec = requestJSON(t, h, http.MethodPost, path, body)
		}
		e := decode[Error](t, rec)
		if rec.Code != http.StatusConflict || e.Code == nil || *e.Code != ErrorCodeConfirmationRequired || e.Plan == nil {
			t.Fatalf("body %q: %d %+v", body, rec.Code, e)
		}
		p := *e.Plan
		if p.Service != "db" || p.Action != ServiceActionRestart || len(p.Affected) != 2 ||
			p.Affected[0] != (AffectedService{Service: "db", Effect: AffectedServiceEffectRestart, Reason: AffectedServiceReasonTarget}) ||
			p.Affected[1] != (AffectedService{Service: "app", Effect: AffectedServiceEffectRestart, Reason: AffectedServiceReasonDependent}) {
			t.Errorf("body %q: plan = %+v", body, p)
		}
	}
	if got := ops.List("app"); len(got) != 0 {
		t.Fatalf("operations started without confirmation: %+v", got)
	}

	// Confirming exactly the affected services, in any order, runs it.
	rec := requestJSON(t, h, http.MethodPost, path, `{"confirm":["app","db"]}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("confirmed = %d: %s", rec.Code, rec.Body)
	}
	op := decode[Operation](t, rec)
	if op.Services == nil || strings.Join(*op.Services, ",") != "db,app" {
		t.Errorf("services = %v", op.Services)
	}
	waitDone(t, ops, op.Id)
}

func TestRunServiceActionErrors(t *testing.T) {
	h, _ := newOpsServer(t, runner{})
	tests := []struct {
		path string
		want int
		code ErrorCode
	}{
		{"/api/v1/stacks/app/services/solo/actions/down", http.StatusBadRequest, ErrorCodeInvalidAction},
		{"/api/v1/stacks/Bad.Name/services/solo/actions/start", http.StatusBadRequest, ErrorCodeInvalidName},
		{"/api/v1/stacks/app/services/-rm/actions/start", http.StatusBadRequest, ErrorCodeInvalidService},
		{"/api/v1/stacks/nope/services/solo/actions/start", http.StatusNotFound, ErrorCodeNotFound},
		{"/api/v1/stacks/app/services/missing/actions/start", http.StatusNotFound, ErrorCodeServiceNotFound},
		{"/api/v1/stacks/ext/services/solo/actions/stop", http.StatusConflict, ErrorCodeStackUnmanaged},
	}
	for _, tt := range tests {
		rec := request(t, h, http.MethodPost, tt.path)
		e := decode[Error](t, rec)
		if rec.Code != tt.want || e.Code == nil || *e.Code != tt.code {
			t.Errorf("%s = %d %+v, want %d %s", tt.path, rec.Code, e, tt.want, tt.code)
		}
	}
}

func TestServiceActionsNeverPreempt(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	h, ops := newOpsServer(t, runner{block: block})

	// A service action is rejected while a stack action runs, even stop.
	start := decode[Operation](t, request(t, h, http.MethodPost, "/api/v1/stacks/app/actions/start"))
	rec := request(t, h, http.MethodPost, "/api/v1/stacks/app/services/solo/actions/stop")
	e := decode[Error](t, rec)
	if rec.Code != http.StatusConflict || *e.Code != ErrorCodeStackBusy || *e.Operation != start.Id {
		t.Errorf("service stop while busy = %d %+v", rec.Code, e)
	}
	stop := decode[Operation](t, request(t, h, http.MethodPost, "/api/v1/stacks/app/actions/stop"))
	waitDone(t, ops, stop.Id)

	// A running service action shows in the stack and is canceled by a
	// stack stop.
	svc := decode[Operation](t, request(t, h, http.MethodPost, "/api/v1/stacks/app/services/blocker/actions/start"))
	detail := decode[StackDetail](t, request(t, h, http.MethodGet, "/api/v1/stacks/app"))
	if a := detail.ActiveOperation; a == nil || a.Id != svc.Id || a.Service == nil || *a.Service != "blocker" {
		t.Errorf("activeOperation = %+v", a)
	}
	rec = request(t, h, http.MethodPost, "/api/v1/stacks/app/actions/stop")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("stack stop = %d: %s", rec.Code, rec.Body)
	}
	stop = decode[Operation](t, rec)
	if s := waitDone(t, ops, svc.Id); s.Status != operations.StatusCanceled || s.CanceledBy != stop.Id {
		t.Errorf("service action = %+v, want canceled by %s", s, stop.Id)
	}
	waitDone(t, ops, stop.Id)
}
