package milclive

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// capAt is when testdata/milestones.json and live_batch.json were captured:
// 2026-09-26 12:36 PM PT, with games pre-toss, at the toss, in a rain delay,
// at an innings break, mid-innings and finished. Every clock in these tests
// comes from a capture, never from time.Now.
var capAt = time.Date(2026, 9, 26, 19, 36, 0, 0, time.UTC)

// chaseAt is when the *_2004z.json pair was captured, 28 minutes later:
// Atlanta Lightning were chasing 206 ("require 178 runs from 91 balls").
var chaseAt = time.Date(2026, 9, 26, 20, 4, 0, 0, time.UTC)

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// games decodes a capture pair into games keyed by the first eight
// characters of the milestone id.
func games(t *testing.T, msFile, liveFile string, at time.Time) map[string]Game {
	t.Helper()
	fx, _, dropped := ParseMilestones(readFile(t, msFile))
	if dropped != 0 {
		t.Fatalf("%s: %d milestones dropped", msFile, dropped)
	}
	lives, dropped := ParseLiveBatch(readFile(t, liveFile), at)
	if dropped != 0 {
		t.Fatalf("%s: %d live items dropped", liveFile, dropped)
	}
	out := map[string]Game{}
	for _, f := range fx {
		out[f.ID[:8]] = NewGame(f, lives[f.ID])
	}
	return out
}

func captureGames(t *testing.T) map[string]Game {
	return games(t, "milestones.json", "live_batch.json", capAt)
}

func TestDecodeMilestones(t *testing.T) {
	fx, cursor, dropped := ParseMilestones(readFile(t, "milestones.json"))
	if len(fx) != 21 || dropped != 0 || cursor != "" {
		t.Fatalf("fixtures=%d dropped=%d cursor=%q, want 21, 0, empty", len(fx), dropped, cursor)
	}
	by := map[string]Fixture{}
	for _, f := range fx {
		by[f.ID[:8]] = f
	}
	chi := by["bfd5348c"]
	if chi.Home != "Chicago Kingsmen" || chi.Away != "Chicago Tigers" {
		t.Errorf("title split into %q / %q", chi.Home, chi.Away)
	}
	if chi.EventTicker != "KXT20MATCH-26SEP261500CHITIGCHKI" {
		t.Errorf("event ticker = %q", chi.EventTicker)
	}
	if m := by["99cf27e2"]; m.MaxOv != 16 || m.StatusTarget != 99 {
		t.Errorf("Manhattan Yorkers v New Jersey Stallions: MaxOv %d Target %d, want 16 and 99", m.MaxOv, m.StatusTarget)
	}
	if p := by["041168aa"]; p.MaxOv != 10 {
		t.Errorf("The Philadelphians v NYC Titans: MaxOv %d, want 10", p.MaxOv)
	}
}

// One bad item costs that item. The endpoints are undocumented, and a
// single odd record must never empty the list.
func TestBadItemsAreDroppedAlone(t *testing.T) {
	var page struct {
		Cursor     string            `json:"cursor"`
		Milestones []json.RawMessage `json:"milestones"`
	}
	if err := json.Unmarshal(readFile(t, "milestones.json"), &page); err != nil {
		t.Fatal(err)
	}
	var sample map[string]any
	if err := json.Unmarshal(page.Milestones[0], &sample); err != nil {
		t.Fatal(err)
	}
	inject := func(id string, mutate func(m map[string]any)) {
		b, _ := json.Marshal(sample)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		m["id"] = id
		mutate(m)
		raw, _ := json.Marshal(m)
		page.Milestones = append(page.Milestones, raw)
	}
	inject("bad-tickers", func(m map[string]any) { m["primary_event_tickers"] = 5 })
	inject("bad-league", func(m map[string]any) { m["details"].(map[string]any)["league"] = "Major League Cricket" })
	inject("bad-type", func(m map[string]any) { m["type"] = "football_game" })
	body, _ := json.Marshal(page)
	fx, _, dropped := ParseMilestones(body)
	if len(fx) != 21 {
		t.Errorf("milestones: %d survived, want the 21 good ones", len(fx))
	}
	if dropped != 1 {
		t.Errorf("milestones: dropped=%d, want 1 (the undecodable one; the others are filtered)", dropped)
	}
	for _, f := range fx {
		if strings.HasPrefix(f.ID, "bad-") {
			t.Errorf("injected item %s survived", f.ID)
		}
	}

	var live struct {
		LiveDatas []json.RawMessage `json:"live_datas"`
	}
	if err := json.Unmarshal(readFile(t, "live_batch.json"), &live); err != nil {
		t.Fatal(err)
	}
	var item map[string]any
	_ = json.Unmarshal(live.LiveDatas[0], &item)
	item["milestone_id"] = "bad-score"
	item["details"].(map[string]any)["home_score"] = map[string]any{}
	raw, _ := json.Marshal(item)
	live.LiveDatas = append(live.LiveDatas, raw)
	lbody, _ := json.Marshal(live)
	lives, ldropped := ParseLiveBatch(lbody, capAt)
	if len(lives) != 21 || ldropped != 1 {
		t.Errorf("live: %d survived, %d dropped; want 21 and 1", len(lives), ldropped)
	}
	if _, ok := lives["bad-score"]; ok {
		t.Error("a live item whose score is an object survived")
	}
}

// bookmakerWord matches the provider's bookmaker fields as whole words:
// "last_play" is fine, "lay_price" is not.
var bookmakerWord = regexp.MustCompile(`(?i)(^|[^a-z])(probability|price|prices|odds|back|lay)([^a-z]|$)`)

// camelWords splits "LastPlay" into "Last Play" so a Go field name can be
// read with the same rule as a json tag.
func camelWords(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// The provider's bookmaker win probability is neither the market nor our
// model, and the owner's rule is that it is never shown. It is never
// decoded, so it cannot be: no decode struct has a field for it.
func TestNoBookmakerFieldsDecoded(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(rt reflect.Type, path string)
	walk = func(rt reflect.Type, path string) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Slice || rt.Kind() == reflect.Map {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || seen[rt] || rt.PkgPath() == "time" {
			return
		}
		seen[rt] = true
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			name := camelWords(f.Name)
			if tag := strings.Split(f.Tag.Get("json"), ",")[0]; tag != "" {
				name = tag
			}
			if bookmakerWord.MatchString(name) {
				t.Errorf("%s.%s decodes %q, a bookmaker field", path, f.Name, name)
			}
			walk(f.Type, path+"."+f.Name)
		}
	}
	for _, v := range []any{rawMilestone{}, rawLive{}, Fixture{}, Side{}, Live{}, Game{}} {
		walk(reflect.TypeOf(v), reflect.TypeOf(v).Name())
	}

	// And nothing from the capture's bookmaker block survives into a Game.
	for id, g := range captureGames(t) {
		b, err := json.Marshal(g)
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if m := bookmakerWord.FindString(s); m != "" {
			t.Errorf("game %s marshals a bookmaker word %q", id, m)
		}
		for _, v := range []string{"0.748", "1.26", "0.252"} {
			if strings.Contains(s, v) {
				t.Errorf("game %s carries the bookmaker value %s", id, v)
			}
		}
	}
}

func TestParseOvers(t *testing.T) {
	good := map[string]int{"19.3": 117, "20": 120, "15.5": 95, "0": 0, "13.1": 79, "7.0": 42}
	for in, want := range good {
		if got, ok := ParseOvers(in); !ok || got != want {
			t.Errorf("ParseOvers(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"15.7", "5.83", "", "abc", "6.6", "-1", "100"} {
		if got, ok := ParseOvers(in); ok {
			t.Errorf("ParseOvers(%q) = %d accepted; the ball digit never exceeds 5", in, got)
		}
	}
}

// The ticker encodes the start in New York time. tzdata is embedded, so
// this holds on a server with no zone files and TZ=UTC as much as on a Mac.
func TestTickerStart(t *testing.T) {
	cases := map[string]time.Time{
		"KXT20MATCH-26SEP261500CHITIGCHKI": time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC),
		"KXT20MATCH-26SEP270500GHANGA-A":   time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC),
		"kxt20match-26sep271500stloamchki": time.Date(2026, 9, 27, 19, 0, 0, 0, time.UTC),
		"KXT20MATCH-26SEP281400SDSRLOANLA": time.Date(2026, 9, 28, 18, 0, 0, 0, time.UTC),
	}
	for in, want := range cases {
		got, ok := TickerStart(in)
		if !ok || !got.Equal(want) {
			t.Errorf("TickerStart(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "garbage", "KXT20MATCH-26XXX261500ABC", "KXT20MATCH"} {
		if _, ok := TickerStart(in); ok {
			t.Errorf("TickerStart(%q) accepted garbage", in)
		}
	}
}

func TestCanon(t *testing.T) {
	if Canon("St. Louis Americans") != Canon("St Louis Americans") {
		t.Errorf("%q != %q", Canon("St. Louis Americans"), Canon("St Louis Americans"))
	}
	if Canon("Ft Lauderdale") != "fort lauderdale" {
		t.Errorf("ft: %q", Canon("Ft Lauderdale"))
	}
	if Canon("How are the Chicago Kingsmen doing?") != "how are the chicago kingsmen doing" {
		t.Errorf("sentence: %q", Canon("How are the Chicago Kingsmen doing?"))
	}
}
