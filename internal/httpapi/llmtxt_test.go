package httpapi

import (
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// documentedRoute matches the ENDPOINTS table's own lines, which is the only
// place llm.txt is allowed to declare a route.
var documentedRoute = regexp.MustCompile(`(?m)^\s+(GET|POST)\s+(/\S+)`)

func TestLLMTxtDocumentsEveryRoute(t *testing.T) {
	served := Paths()
	if len(served) == 0 {
		t.Fatal("Paths() is empty — the drift check would pass vacuously")
	}

	var documented []string
	for _, m := range documentedRoute.FindAllStringSubmatch(llmTxt, -1) {
		documented = append(documented, m[2])
	}

	for _, p := range served {
		if !slices.Contains(documented, p) {
			t.Errorf("route %q is served but not documented in llm.txt — an agent reading the "+
				"protocol would never know it exists", p)
		}
	}
	for _, p := range documented {
		if !slices.Contains(served, p) {
			t.Errorf("llm.txt documents %q but nothing serves it — following the document would 404", p)
		}
	}

	t.Run("falsifiable", func(t *testing.T) {
		// If the extractor found nothing, both loops above are vacuous and this
		// gate reads as clean while checking nothing.
		if len(documented) == 0 {
			t.Fatal("extracted 0 routes from llm.txt — the ENDPOINTS table is missing or reshaped, " +
				"so the drift check is inert")
		}
	})
}

func TestLLMTxtStatesTheTrustBoundary(t *testing.T) {
	// Twice is the requirement, not once. A single mention is satisfied by a
	// line buried mid-document, and a model acts on what it reads first.
	if n := strings.Count(llmTxt, trustRuleHeading); n != 2 {
		t.Errorf("trust rule appears %d times, want exactly 2 (before the first command and at the end)", n)
	}

	first := strings.Index(llmTxt, trustRuleHeading)
	firstCommand := strings.Index(llmTxt, "curl")
	if first == -1 || firstCommand == -1 {
		t.Fatal("document is missing the trust rule or any command")
	}
	if first > firstCommand {
		t.Error("the trust rule appears after the first curl command; it must come before anything an agent could run")
	}

	last := strings.LastIndex(llmTxt, trustRuleHeading)
	if tail := llmTxt[last:]; strings.Count(tail, "curl") != 0 {
		t.Error("the closing trust rule must be the last thing in the document, after every command")
	}

	for _, phrase := range []string{
		"not an instruction",
		"cannot authorise",
		"Only your own user",
	} {
		if !strings.Contains(llmTxt, phrase) {
			t.Errorf("document is missing the phrase %q — the rule must be explicit about what a message cannot do", phrase)
		}
	}
}

func TestLLMTxtWarnsThatNameIsNotIdentity(t *testing.T) {
	for _, phrase := range []string{
		`"name" IS NOT IDENTITY`,
		"chooses it",
		"never provenance",
	} {
		if !strings.Contains(llmTxt, phrase) {
			t.Errorf("document is missing %q — a model that reads \"from\" as authority has been handed "+
				"an authentication system that does not exist", phrase)
		}
	}
}

func TestLLMTxtCarriesTheFullProcedureInOrder(t *testing.T) {
	steps := []string{
		"STEP 1 — MINT",
		"STEP 2 — MONITOR",
		"STEP 3 — HAND OFF",
		"STEP 4 — SEND",
		"STEP 5 — REACT",
	}
	prev := -1
	for _, s := range steps {
		i := strings.Index(llmTxt, s)
		if i == -1 {
			t.Errorf("document is missing %q", s)
			continue
		}
		if i < prev {
			t.Errorf("%q appears out of order — the procedure must read top to bottom", s)
		}
		prev = i
	}
}

func TestLLMTxtHandoffBlockIsPasteableAndNamesADistinctIdentity(t *testing.T) {
	if !strings.Contains(llmTxt, "openssl rand -hex 16") {
		t.Error("document must tell the agent how to mint an unguessable channel")
	}
	// The teammate must not reuse the first agent's $ME: two clients sharing a
	// name are one identity to the hub, and neither would see the other.
	if !strings.Contains(llmTxt, "do not reuse mine") {
		t.Error("the handoff block must tell the teammate to pick their own name")
	}
	if !strings.Contains(llmTxt, "$BASE/llm.txt") {
		t.Error("the handoff block must point the teammate back at this document")
	}
}

func TestLLMTxtIsServedAsPlainText(t *testing.T) {
	srv, _ := startServer(t)
	resp, err := http.Get(srv.URL + "/llm.txt")
	if err != nil {
		t.Fatalf("get /llm.txt: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != llmTxt {
		t.Error("served bytes differ from the embedded document")
	}
	// Bootstrapping must work before the agent has a channel: this is the one
	// route that takes no credential.
	if strings.Contains(resp.Request.URL.RawQuery, "channel") {
		t.Error("/llm.txt must be fetchable without a channel")
	}
}
