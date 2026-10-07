package history

import (
	"os"
	"strings"
	"testing"

	"github.com/asaraog/mcp-cricket/internal/history/histtest"
)

// Fixture: two real 2026 MLC matches — Freedom v Unicorns (Jul 16) and
// Knight Riders v Freedom (Jul 18, the season's last game) — plus the
// 2024 World Cup matches histtest adds to a copy of it.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "histfixture")
	if err != nil {
		panic(err)
	}
	path, err := histtest.Extend("testdata/fixture.db", dir)
	if err != nil {
		panic(err)
	}
	os.Setenv("HISTORY_DB", path)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func TestEnabledAndTeams(t *testing.T) {
	if !Enabled() {
		t.Fatal("fixture db should enable history")
	}
	got := TeamsIn("how did the Washington Freedom do against the San Francisco Unicorns?")
	if len(got) != 2 {
		t.Fatalf("TeamsIn = %v", got)
	}
}

func TestFindMatchAndScorecard(t *testing.T) {
	m, ok := FindMatch("what happened in the Freedom Unicorns game in 2026",
		[]string{"Washington Freedom", "San Francisco Unicorns"})
	if !ok {
		t.Fatal("match should resolve")
	}
	if m.Date != "2026-07-16" {
		t.Fatalf("wrong match: %+v", m)
	}
	card := Scorecard(m)
	if !strings.Contains(card, "Innings 1") || !strings.Contains(card, "bat:") {
		t.Fatalf("thin scorecard:\n%s", card)
	}
}

func TestFinalHeuristicPicksLatest(t *testing.T) {
	m, ok := FindMatch("who won the 2026 final", []string{"Washington Freedom"})
	if !ok {
		t.Fatal("final should resolve")
	}
	if m.Date != "2026-07-18" {
		t.Fatalf("final heuristic picked %s", m.Date)
	}
}

func TestOverDetail(t *testing.T) {
	m, _ := FindMatch("freedom unicorns 2026", []string{"Washington Freedom", "San Francisco Unicorns"})
	od := OverDetail(m, "who bowled the 4th over?")
	if !strings.Contains(od, "Over 4 of innings 1: bowled by") {
		t.Fatalf("over detail:\n%q", od)
	}
}

func TestUnknownTeamFailsQuiet(t *testing.T) {
	if _, ok := FindMatch("anything", []string{"Fake Team FC"}); ok {
		t.Fatal("unknown team must not resolve")
	}
}

// Prod, 2026-10-06, "who won the 2024 T20 world cup final": "New Zealand
// won the 2024 Women's T20 World Cup final." No team was named, and only
// a league could resolve a match without one. The tournament resolves it
// now: the last match of the event and year is the final, the men's
// unless the question says women's, and a question about who took it
// resolves without the word "final".
func TestTournamentFinalResolvesWithoutATeam(t *testing.T) {
	for _, c := range []struct{ msg, date, event string }{
		{"who won the 2024 T20 world cup final", "2024-06-29", "ICC Men's T20 World Cup"},
		{"who won the 2024 T20 world cup", "2024-06-29", "ICC Men's T20 World Cup"},
		{"how many runs did Rohit Sharma score in the 2024 T20 world cup final", "2024-06-29", "ICC Men's T20 World Cup"},
		{"who won the 2024 women's T20 world cup final", "2024-10-20", "ICC Women's T20 World Cup"},
		{"2024 womens t20 wc winner", "2024-10-20", "ICC Women's T20 World Cup"},
	} {
		m, ok := FindMatch(c.msg, nil)
		if !ok {
			t.Errorf("%q resolved nothing", c.msg)
			continue
		}
		if m.Date != c.date || m.Event != c.event {
			t.Errorf("%q: %s %q, want %s %q", c.msg, m.Date, m.Event, c.date, c.event)
		}
	}
	// A tournament with nothing asked about its outcome is no one match,
	// and a tournament the archive does not hold resolves nothing.
	for _, msg := range []string{
		"best bowling figures at the 2024 T20 world cup",
		"who won the 2024 champions trophy final",
		"who won the 2024 world cup final",
	} {
		if m, ok := FindMatch(msg, nil); ok {
			t.Errorf("%q resolved %s %q", msg, m.Date, m.Event)
		}
	}
}

// The hosted archive tool for "India v South Africa 2024 T20 World Cup
// final" returned the November T20I of India's tour (Tilak Varma 120):
// with teams named, only the year filtered. The tournament named keeps
// the teams' other meetings out; the year alone still picks the latest.
func TestTournamentKeepsTheTeamsOtherMeetingsOut(t *testing.T) {
	names := TeamsIn("India v South Africa 2024 T20 World Cup final")
	if len(names) != 2 {
		t.Fatalf("TeamsIn = %v", names)
	}
	for _, c := range []struct {
		msg   string
		names []string
		date  string
	}{
		{"India v South Africa 2024 T20 World Cup final", names, "2024-06-29"},
		{"India 2024 T20 World Cup final", []string{"India"}, "2024-06-29"},
		{"India v South Africa 2024", names, "2024-11-15"},
		{"India v South Africa 2024 final", names, "2024-11-15"},
	} {
		m, ok := FindMatch(c.msg, c.names)
		if !ok {
			t.Errorf("%q resolved nothing", c.msg)
			continue
		}
		if m.Date != c.date {
			t.Errorf("%q: %s %q, want %s", c.msg, m.Date, m.Event, c.date)
		}
	}
}

// Prod, 2026-10-06, "how many runs did Rohit Sharma score in the 2024 T20
// world cup final" was answered "9 off 5", then "I don't have the
// per-match scorecard", then "76 off 59" (Kohli's). Scorecard lists three
// batters an innings and Rohit was fourth, so his line was never in the
// block. PlayerLine finds the player the question names, under the
// archive's name.
func TestPlayerLineNamesTheQuestionsPlayer(t *testing.T) {
	m, ok := FindMatch("how many runs did Rohit Sharma score in the 2024 T20 world cup final", nil)
	if !ok {
		t.Fatal("final should resolve")
	}
	if card := Scorecard(m); strings.Contains(card, "RG Sharma") {
		t.Fatalf("precondition: the top three hold Rohit:\n%s", card)
	}
	rohit := "  bat: RG Sharma 9 off 5, out caught\n"
	for _, c := range []struct{ msg, want string }{
		{"how many runs did Rohit Sharma score in the 2024 T20 world cup final", rohit},
		{"RG Sharma in the 2024 t20 world cup final?", rohit},
		{"what did Sharma do in the final", rohit},
		{"Rohit Sharma's score in the final", rohit},
		{"Virat Kohli in the 2024 T20 World Cup final", "  bat: V Kohli 76 off 59, out caught\n"},
		{"Quinton de Kock's score in the 2024 T20 World Cup final", "  bat: Q de Kock 39 off 31, out caught\n"},
		// Klaasen's two wides are Bumrah's: 52 and 4 and two extras.
		{"how did Bumrah bowl in the 2024 T20 World Cup final", "  bowl: JJ Bumrah 2 wickets for 58\n"},
		{"Axar Patel in the 2024 T20 World Cup final", "  bat: AR Patel 47 off 31, out run out\n  bowl: AR Patel 1 wickets for 39\n"},
		// No one named, and a Sharma who was not in the match.
		{"who won the 2024 T20 world cup final", ""},
		{"how many did Abhishek Sharma score in the 2024 T20 world cup final", ""},
	} {
		if got := PlayerLine(m, c.msg); got != c.want {
			t.Errorf("%q:\n got: %q\nwant: %q", c.msg, got, c.want)
		}
	}
}

// keyNames is the players' names in order, comma-joined.
func keyNames(ks []KeyPlayer) string {
	var names []string
	for _, k := range ks {
		names = append(names, k.Name)
	}
	return strings.Join(names, ", ")
}

// Prod, 2026-10-05, USA v UAE selected, "can you create dream11 teams":
// the reader wanted each side's in-form names and was told the service
// does not do that. TeamKeyPlayers is a side's leading batters and
// bowlers over its last archived games of one format and one gender. The
// fixture holds three India men's T20s (the 2024 T20 World Cup semi-final
// and final, and the November T20I, where Tilak Varma's 120 outscores
// Kohli's 76 in the final), a women's T20I and a men's ODI.
func TestTeamKeyPlayersOverRecentGames(t *testing.T) {
	if !Enabled() || !hasGender {
		t.Fatal("precondition: the extended fixture carries the gender column")
	}
	bat, bowl, ok := TeamKeyPlayers("India", "t20", "male", 10)
	if !ok || len(bat) != 5 || len(bowl) != 4 {
		t.Fatalf("India's three men's T20s: bat %+v, bowl %+v", bat, bowl)
	}
	if got := keyNames(bat); got != "N Tilak Varma, SV Samson, V Kohli, RG Sharma, SA Yadav" {
		t.Errorf("India batters: %s", got)
	}
	// Kohli's 76 is off 59 balls, the three wides left out: strike rate
	// 128.8. Rohit's 66 is two innings, 57 in the semi-final and 9 in the
	// final.
	if k := bat[2]; k.Runs != 76 || k.Balls != 59 || k.Innings != 1 || k.SR < 128.8 || k.SR > 128.9 {
		t.Errorf("Kohli: %+v", k)
	}
	if k := bat[3]; k.Runs != 66 || k.Balls != 44 || k.Innings != 2 {
		t.Errorf("Rohit: %+v", k)
	}
	// Axar Patel's three: Buttler and Bairstow in the semi-final, de Kock
	// in the final, over 48 legal balls for 62. Bumrah's two cost 58, the
	// two wides Klaasen faced among them, over 32 legal balls.
	if got := keyNames(bowl); got != "AR Patel, Arshdeep Singh, HH Pandya, JJ Bumrah" {
		t.Errorf("India bowlers: %s", got)
	}
	if k := bowl[0]; k.Wkts != 3 || k.Balls != 48 || k.Innings != 2 || k.Econ < 7.74 || k.Econ > 7.76 {
		t.Errorf("Patel: %+v", k)
	}
	if k := bowl[3]; k.Wkts != 2 || k.Balls != 32 || k.Econ < 10.87 || k.Econ > 10.88 {
		t.Errorf("Bumrah: %+v", k)
	}
	// Only the last men's T20, named as a reader might: the November
	// T20I, where Kohli did not bat.
	bat, bowl, ok = TeamKeyPlayers("india", "T20I", "men", 1)
	if !ok || keyNames(bat) != "N Tilak Varma, SV Samson" || bat[0].Runs != 120 || keyNames(bowl) != "Arshdeep Singh" {
		t.Errorf("India's last men's T20: bat %+v, bowl %+v", bat, bowl)
	}
	// The other side of the same games: South Africa's batters and
	// Maharaj's three in the final. The women's World Cup final is South
	// Africa's too, and Wolvaardt and de Klerk are not in the men's list.
	bat, bowl, ok = TeamKeyPlayers("South Africa", "t20", "male", 10)
	if !ok || keyNames(bat) != "T Stubbs, H Klaasen, Q de Kock, DA Miller, A Markram" || bat[0].Runs != 74 ||
		keyNames(bowl) != "K Maharaj, A Nortje, M Jansen" || bowl[0].Wkts != 3 {
		t.Errorf("South Africa: bat %+v, bowl %+v", bat, bowl)
	}
	if _, _, ok := TeamKeyPlayers("Narnia", "t20", "male", 10); ok {
		t.Error("a team the archive does not hold has key players")
	}
	if _, _, ok := TeamKeyPlayers("India", "t20", "male", 0); ok {
		t.Error("zero games has key players")
	}
}

// Read over every game, India's list is topped by an ODI 210 and holds a
// women's 80, and its bowling is topped by a women's four-for: Cricsheet
// names both of India's sides "India". Each format and gender reads its
// own games, and TeamForm keeps the same ones.
func TestTeamKeyPlayersKeepOneFormatAndGender(t *testing.T) {
	bat, bowl, ok := TeamKeyPlayers("India", "", "", 10)
	if !ok || keyNames(bat) != "Ishan Kishan, N Tilak Varma, SV Samson, S Mandhana, V Kohli" ||
		keyNames(bowl) != "SS Vastrakar, AR Patel, SN Thakur, Arshdeep Singh, HH Pandya" {
		t.Errorf("India, every game: bat %s; bowl %s", keyNames(bat), keyNames(bowl))
	}
	for _, c := range []struct {
		format, gender string
		bat, bowl      string
	}{
		{"t20", "female", "S Mandhana", "SS Vastrakar"},
		{"", "female", "S Mandhana", "SS Vastrakar"},
		{"odi", "male", "Ishan Kishan", "SN Thakur"},
		{"ODI", "", "Ishan Kishan", "SN Thakur"},
	} {
		bat, bowl, ok := TeamKeyPlayers("India", c.format, c.gender, 10)
		if !ok || keyNames(bat) != c.bat || keyNames(bowl) != c.bowl {
			t.Errorf("India %q %q: bat %s; bowl %s", c.format, c.gender, keyNames(bat), keyNames(bowl))
		}
	}
	// Mandhana's 80 off 50 and Vastrakar's 4 for 80 off 77 legal balls;
	// Kishan's 210 off 131 and Thakur's 3 for 97 off 97.
	bat, bowl, _ = TeamKeyPlayers("India", "t20", "female", 10)
	if len(bat) != 1 || bat[0].Runs != 80 || bat[0].Balls != 50 || bat[0].SR != 160 ||
		len(bowl) != 1 || bowl[0].Wkts != 4 || bowl[0].Balls != 77 || bowl[0].Econ < 6.23 || bowl[0].Econ > 6.24 {
		t.Errorf("India's women: bat %+v, bowl %+v", bat, bowl)
	}
	bat, bowl, _ = TeamKeyPlayers("India", "odi", "male", 10)
	if len(bat) != 1 || bat[0].Runs != 210 || bat[0].Balls != 131 ||
		len(bowl) != 1 || bowl[0].Wkts != 3 || bowl[0].Balls != 97 || bowl[0].Econ != 6 {
		t.Errorf("India's men's ODI: bat %+v, bowl %+v", bat, bowl)
	}
	if _, _, ok := TeamKeyPlayers("India", "test", "male", 10); ok {
		t.Error("India has no Test in the fixture and has key players for one")
	}
	for _, c := range []struct {
		format, gender string
		dates          string
	}{
		{"", "", "2024-11-15, 2024-06-29, 2024-06-27, 2024-04-28, 2022-12-10"},
		{"t20", "male", "2024-11-15, 2024-06-29, 2024-06-27"},
		{"t20", "female", "2024-04-28"},
		{"odi", "male", "2022-12-10"},
		{"test", "", ""},
	} {
		rows, _ := TeamForm("India", c.format, c.gender, 10)
		var dates []string
		for _, r := range rows {
			dates = append(dates, r.Date)
		}
		if got := strings.Join(dates, ", "); got != c.dates {
			t.Errorf("TeamForm India %q %q: %s, want %s", c.format, c.gender, got, c.dates)
		}
	}
}

// A file built before the gender column, the v1 release and the wide-only
// rebuild of 2026-10-06, has none, and the event name stands in: "India
// Women tour of Bangladesh" and "ICC Women's T20 World Cup" say whose
// games they are.
func TestOldArchiveReadsGenderOffTheEventName(t *testing.T) {
	path, err := histtest.ExtendOld("testdata/fixture.db", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old, cols, err := openFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cols.gender || cols.wide {
		t.Fatalf("the old-shape fixture reports %+v", cols)
	}
	savedDB, savedWide, savedGender := db, hasWide, hasGender
	db, hasWide, hasGender = old, cols.wide, cols.gender
	t.Cleanup(func() {
		db, hasWide, hasGender = savedDB, savedWide, savedGender
		old.Close()
	})
	bat, bowl, ok := TeamKeyPlayers("India", "t20", "male", 10)
	if !ok || keyNames(bat) != "N Tilak Varma, SV Samson, V Kohli, RG Sharma, SA Yadav" ||
		keyNames(bowl) != "AR Patel, Arshdeep Singh, HH Pandya, JJ Bumrah" {
		t.Fatalf("India's men's T20s on the old shape: bat %s; bowl %s", keyNames(bat), keyNames(bowl))
	}
	// Every row is a ball on this shape, as it was: Kohli's three wides
	// are three more.
	if bat[2].Balls != 62 {
		t.Errorf("Kohli on the old shape: %+v", bat[2])
	}
	bat, bowl, ok = TeamKeyPlayers("South Africa", "t20", "male", 10)
	if !ok || keyNames(bat) != "T Stubbs, H Klaasen, Q de Kock, DA Miller, A Markram" || keyNames(bowl) != "K Maharaj, A Nortje, M Jansen" {
		t.Errorf("South Africa's men's T20s on the old shape: bat %s; bowl %s", keyNames(bat), keyNames(bowl))
	}
	bat, bowl, ok = TeamKeyPlayers("India", "t20", "female", 10)
	if !ok || keyNames(bat) != "S Mandhana" || keyNames(bowl) != "SS Vastrakar" {
		t.Errorf("India's women on the old shape: bat %s; bowl %s", keyNames(bat), keyNames(bowl))
	}
	if rows, ok := TeamForm("South Africa", "", "women", 10); !ok || len(rows) != 1 || rows[0].Opponent != "New Zealand" {
		t.Errorf("South Africa's women on the old shape: %+v", rows)
	}
	// The leaders read their gender off the event name here too.
	if ls, ok := Leaders("t20", "2024", "batting", "male", 10); !ok || leaderNames(ls) != menOf2024 {
		t.Errorf("2024 t20 men's leaders on the old shape: %s", leaderNames(ls))
	}
	if ls, ok := Leaders("t20", "2024", "batting", "female", 50); !ok || leaderNames(ls) != womenOf2024 {
		t.Errorf("2024 t20 women's leaders on the old shape: %s", leaderNames(ls))
	}
	for _, c := range bigBashLeaders {
		if ls, ok := Leaders("bbl", "", "batting", c.gender, 10); !ok || leaderNames(ls) != c.want {
			t.Errorf("bbl %q on the old shape: %s", c.gender, leaderNames(ls))
		}
	}
	// The T20Is are told from the domestic T20s by the sides' names,
	// which the old shape has too.
	if ls, ok := Leaders("t20i", "2024", "batting", "male", 10); !ok || leaderNames(ls) != t20iMenOf2024 {
		t.Errorf("2024 T20I men's leaders on the old shape: %s", leaderNames(ls))
	}
}

// The fixture's Big Bash runs: Mooney and Matthews in the women's game,
// Lynn and Finch in the men's, all four under "bbl".
var bigBashLeaders = []struct{ gender, want string }{
	{"", "BL Mooney, HK Matthews, CA Lynn, AJ Finch"},
	{"male", "CA Lynn, AJ Finch"},
	{"female", "BL Mooney, HK Matthews"},
}

// leaderNames is the leaders' names in order, comma-joined.
func leaderNames(ls []Leader) string {
	var names []string
	for _, l := range ls {
		names = append(names, l.Name)
	}
	return strings.Join(names, ", ")
}

// The fixture's 2024 "t20" games by runs: the men's ten, with the Syed
// Mushtaq Ali Trophy's V Solanki third; the men's ten of the T20Is
// alone; and every woman, all of them in T20Is.
const (
	menOf2024     = "N Tilak Varma, SV Samson, V Solanki, V Kohli, T Stubbs, RG Sharma, H Klaasen, SA Yadav, AR Patel, Q de Kock"
	t20iMenOf2024 = "N Tilak Varma, SV Samson, V Kohli, T Stubbs, RG Sharma, H Klaasen, SA Yadav, AR Patel, Q de Kock, S Dube"
	womenOf2024   = "S Mandhana, AC Kerr, Nigar Sultana, BM Halliday, L Wolvaardt, Murshida Khatun, T Brits, Shorna Akter, Sobhana Mostary"
)

// histgen.py files every country's T20Is under "t20", men's and women's
// together, and the 2024 list read S Mandhana's 80 third, after Samson's
// 109. Each gender reads its own games.
func TestLeadersKeepOneGender(t *testing.T) {
	all, ok := Leaders("t20", "2024", "batting", "", 3)
	if !ok || leaderNames(all) != "N Tilak Varma, SV Samson, S Mandhana" {
		t.Errorf("2024 t20, both: %s", leaderNames(all))
	}
	if ls, ok := Leaders("t20", "2024", "batting", "male", 10); !ok || leaderNames(ls) != menOf2024 {
		t.Errorf("2024 t20 men's: %s", leaderNames(ls))
	}
	if ls, ok := Leaders("t20", "2024", "batting", "women", 50); !ok || leaderNames(ls) != womenOf2024 {
		t.Errorf("2024 t20 women's: %s", leaderNames(ls))
	}
	// The Women's Big Bash League is filed under "bbl" with the men's.
	// Read with no gender, Mooney leads it, as BL Mooney led staging's
	// "who has the most runs in the big bash" on 2026-10-07.
	for _, c := range bigBashLeaders {
		if ls, ok := Leaders("bbl", "", "batting", c.gender, 10); !ok || leaderNames(ls) != c.want {
			t.Errorf("bbl %q: %s", c.gender, leaderNames(ls))
		}
	}
	// Vastrakar's four lead the women's wickets and are not the men's.
	women, ok := Leaders("t20", "2024", "bowling", "female", 50)
	if !ok || women[0].Name != "SS Vastrakar" || women[0].Wkts != 4 {
		t.Errorf("2024 t20 women's bowling: %+v", women)
	}
	men, _ := Leaders("t20", "2024", "bowling", "male", 50)
	for _, l := range men {
		switch l.Name {
		case "SS Vastrakar", "AC Kerr", "N de Klerk", "Nahida Akter":
			t.Errorf("%s is among the men's bowling leaders", l.Name)
		}
	}
	if !strings.Contains(leaderNames(men), "AR Patel") || !strings.Contains(leaderNames(men), "K Maharaj") {
		t.Errorf("2024 t20 men's bowling: %s", leaderNames(men))
	}
	// The fixture's two MLC games, filed under "fixture.zip" (the zip it
	// was built from), are men's games: the gender changes nothing. Ties
	// in runs have no order, so the lists are compared as runs by name.
	runs := func(ls []Leader) map[string]int {
		m := map[string]int{}
		for _, l := range ls {
			m[l.Name] = l.Runs
		}
		return m
	}
	mlc, _ := Leaders("fixture.zip", "", "batting", "", 50)
	mlcMen, _ := Leaders("fixture.zip", "", "batting", "male", 50)
	a, b := runs(mlc), runs(mlcMen)
	same := len(a) > 0 && len(a) == len(b)
	for name, r := range a {
		if got, ok := b[name]; !ok || got != r {
			same = false
		}
	}
	if !same {
		t.Errorf("MLC: %s\nMLC men's: %s", leaderNames(mlc), leaderNames(mlcMen))
	}
}

// "t20" holds the domestic T20s histgen.py knows no league for, with the
// T20Is: Cricsheet types the Syed Mushtaq Ali Trophy "T20", as it types
// a T20I. Prod and staging, 2026-10-06, "who has the most wickets in T20
// internationals in 2025" read no list, and "t20" would have put the
// states' players among the T20I leaders: V Solanki's 78 third, AA
// Sheth's four wickets first. "t20i" reads the games between two
// international sides.
func TestLeadersT20IKeepsTheDomesticT20sOut(t *testing.T) {
	if ls, ok := Leaders("t20i", "2024", "batting", "male", 10); !ok || leaderNames(ls) != t20iMenOf2024 {
		t.Errorf("2024 T20I men's: %s", leaderNames(ls))
	}
	if ls, ok := Leaders("T20I", "2024", "batting", "female", 50); !ok || leaderNames(ls) != womenOf2024 {
		t.Errorf("2024 T20I women's: %s", leaderNames(ls))
	}
	bowl, ok := Leaders("t20", "2024", "bowling", "male", 50)
	if !ok || bowl[0].Name != "AA Sheth" || bowl[0].Wkts != 4 {
		t.Errorf("2024 t20 men's bowling: %+v", bowl)
	}
	intl, ok := Leaders("t20i", "2024", "bowling", "male", 50)
	if !ok || intl[0].Wkts != 3 || !strings.Contains(leaderNames(intl), "JJ Bumrah") {
		t.Errorf("2024 T20I men's bowling: %+v", intl)
	}
	// Nothing of the Baroda v Sikkim game is in a T20I list, of any
	// season, kind or gender.
	for _, kind := range []string{"batting", "bowling"} {
		ls, ok := Leaders("t20i", "", kind, "", 50)
		if !ok {
			t.Errorf("no T20I %s leaders", kind)
		}
		for _, l := range ls {
			switch l.Name {
			case "V Solanki", "Ankur Malik", "Lee Yong Lepcha", "Ashish Thapa", "Palzor Tamang", "AA Sheth":
				t.Errorf("%s is among the T20I %s leaders", l.Name, kind)
			}
		}
	}
}

// ScopeLabel names the games a block was read over.
func TestScopeLabel(t *testing.T) {
	for _, c := range []struct {
		format, gender string
		n              int
		want           string
	}{
		{"t20", "male", 10, "men's T20s"},
		{"t20", "male", 1, "men's T20"},
		{"odi", "female", 3, "women's ODIs"},
		{"test", "", 2, "Tests"},
		{"", "female", 2, "women's games"},
		{"", "", 5, ""},
		{"hundred", "", 5, ""},
	} {
		if got := ScopeLabel(c.format, c.gender, c.n); got != c.want {
			t.Errorf("ScopeLabel(%q, %q, %d) = %q, want %q", c.format, c.gender, c.n, got, c.want)
		}
	}
}

// ArchiveTeam maps a feed's or a reader's name for a side to the
// archive's: the same name in any case, or the one team whose name holds
// it.
func TestArchiveTeamFindsTheArchivesName(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"india", "India"},
		{"South Africa", "South Africa"},
		{"Unicorns", "San Francisco Unicorns"},
		{"Narnia", ""},
		{"", ""},
	} {
		got, ok := ArchiveTeam(c.in)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("ArchiveTeam(%q) = %q, %v", c.in, got, ok)
		}
	}
}
