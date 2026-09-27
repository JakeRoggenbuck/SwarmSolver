package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/jakeroggenbuck/swarm/internal/graph"
	"github.com/jakeroggenbuck/swarm/internal/state"
)

func TestParsePlan(t *testing.T) {
	p, err := ParsePlan("Sure!\n```json\n{\"move\":\"separate\",\"k\":10,\"pair\":[12,88],\"note\":\"x\"}\n```")
	if err != nil || p.Move != "separate" || p.K != 10 || len(p.Pair) != 2 {
		t.Fatalf("got %+v %v", p, err)
	}
	if _, err := ParsePlan(`{"move":"teleport"}`); err == nil {
		t.Fatal("unknown move accepted")
	}
	if _, err := ParsePlan("no json here"); err == nil {
		t.Fatal("missing JSON accepted")
	}
}

// TestPlannerRequestShape runs the planner against a mock Messages API and
// checks the wire format per model family.
func TestPlannerRequestShape(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = nil
		json.Unmarshal(b, &got)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m","stop_reason":"end_turn",
			"content":[{"type":"text","text":"{\"move\":\"tabucol\",\"start\":\"best\",\"drop_class\":3,\"note\":\"small class\"}"}],
			"usage":{"input_tokens":10,"output_tokens":5}}`)
	}))
	defer srv.Close()
	client := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))
	g := graph.New("t", 3, [][2]int{{0, 1}, {1, 2}})
	p := state.Problem{Name: "t", N: 3, M: 2, KnownUpper: 2}

	for _, tc := range []struct {
		model   string
		wantTmp bool
	}{{"claude-haiku-4-5", true}, {"claude-sonnet-5", false}, {"claude-opus-5", false}} {
		pl := NewClaudePlanner(&client, tc.model, 0.7, "strategist", g, p)
		plan, err := pl.Plan(context.Background(), "digest")
		if err != nil {
			t.Fatalf("%s: %v", tc.model, err)
		}
		if plan.Move != "tabucol" || plan.DropClass == nil || *plan.DropClass != 3 {
			t.Fatalf("%s: plan %+v", tc.model, plan)
		}
		if got["model"] != tc.model {
			t.Errorf("model = %v", got["model"])
		}
		_, hasTemp := got["temperature"]
		if hasTemp != tc.wantTmp {
			t.Errorf("%s: temperature present = %v", tc.model, hasTemp)
		}
		if !tc.wantTmp {
			oc, _ := got["output_config"].(map[string]any)
			if oc["effort"] != "low" {
				t.Errorf("%s: output_config = %v", tc.model, got["output_config"])
			}
		}
		sys, _ := got["system"].([]any)
		if len(sys) != 1 {
			t.Fatalf("%s: system = %v", tc.model, got["system"])
		}
		blk := sys[0].(map[string]any)
		if blk["cache_control"] == nil || !strings.Contains(blk["text"].(string), "SEPARATION") || !strings.Contains(blk["text"].(string), "broadcast") {
			t.Errorf("%s: system block missing cache_control or content", tc.model)
		}
	}
}
