package history

import (
	"os"
	"testing"
	"time"
)

// cricket_partnerships returned "could not read partnerships" for every
// player in production: 3.19s of work against a 3s cap. The analytics
// family needs its own budget, and the web path must not inherit it.
func TestAnalyticsBudgetIsLongerThanTheWebBudget(t *testing.T) {
	os.Unsetenv("HISTORY_QUERY_TIMEOUT")
	os.Unsetenv("HISTORY_ANALYTICS_TIMEOUT")
	web, an := queryDeadline(), analyticsDeadline()
	if web != 3*time.Second {
		t.Errorf("web budget moved: got %v, want 3s — a slow plan must never hang a chat turn", web)
	}
	if an <= web {
		t.Errorf("analytics budget %v does not exceed the web budget %v", an, web)
	}
	// The failing query took 3.19s on the smallest measurement; a career
	// batter in more innings costs more. Headroom, not a hair past it.
	if an < 10*time.Second {
		t.Errorf("analytics budget %v leaves no headroom over the 3.19s that failed", an)
	}
}

func TestBudgetsAreIndependentlyOverridable(t *testing.T) {
	t.Setenv("HISTORY_ANALYTICS_TIMEOUT", "25s")
	if got := analyticsDeadline(); got != 25*time.Second {
		t.Errorf("analytics override ignored: %v", got)
	}
	if got := queryDeadline(); got != 3*time.Second {
		t.Errorf("the analytics override leaked into the web budget: %v", got)
	}
	t.Setenv("HISTORY_QUERY_TIMEOUT", "1s")
	if got := queryDeadline(); got != 1*time.Second {
		t.Errorf("web override ignored: %v", got)
	}
	if got := analyticsDeadline(); got != 25*time.Second {
		t.Errorf("the web override leaked into the analytics budget: %v", got)
	}
}

// A junk value must fall back, never to zero — a zero deadline fails every
// query instantly.
func TestBadDurationFallsBackToTheDefault(t *testing.T) {
	t.Setenv("HISTORY_ANALYTICS_TIMEOUT", "soon")
	if got := analyticsDeadline(); got != 15*time.Second {
		t.Errorf("junk duration did not fall back: %v", got)
	}
	t.Setenv("HISTORY_ANALYTICS_TIMEOUT", "0s")
	if got := analyticsDeadline(); got != 15*time.Second {
		t.Errorf("zero duration accepted; every analytics query would fail: %v", got)
	}
}
