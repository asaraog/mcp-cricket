#!/usr/bin/env python3
"""Build the per-delivery history database from Cricsheet zips.

Usage: python3 scripts/histgen.py <dir-with-zips-or-all_json.zip> <out.db>

EVERY format: T20, ODI/List-A, Tests and domestic multi-day. Player, team
and venue names are interned so the deliveries table stays compact, and
batter/bowler are indexed because analytics queries (phase splits,
leaderboards) filter on them — without those indexes a lookup is a full
scan of millions of rows.

The canonical copy of this script is
asaraog/cricket-history-data/scripts/histgen.py (commit f8ba240), where
the release the server downloads is built. This copy must stay identical
to it: the schema here is what internal/history reads.

The two trailing columns of deliveries, wide and noball, came after
staging answered "who top scored in the 2024 T20 world cup final" with
"Virat Kohli 76 off 62": the server counted rows as balls faced, and
three of Kohli's rows were wides. A wide is not a ball faced; a no-ball
is, but is not a legal ball bowled. The server reads the columns when
the file has them and counts rows on the v1 file, which has neither.
"""
import json, sys, zipfile, glob, os, sqlite3

src, out = sys.argv[1], sys.argv[2]
if os.path.exists(out):
    os.remove(out)
db = sqlite3.connect(out)
db.executescript("""
CREATE TABLE names (id INTEGER PRIMARY KEY, name TEXT UNIQUE);
CREATE TABLE matches (
  id TEXT PRIMARY KEY, date TEXT, league TEXT, overs INTEGER,
  team1 INTEGER, team2 INTEGER, venue TEXT, event TEXT,
  winner INTEGER, result TEXT
);
CREATE TABLE deliveries (
  match_id TEXT, innings INTEGER, over INTEGER, ball INTEGER,
  batter INTEGER, bowler INTEGER,
  runs_batter INTEGER, runs_extras INTEGER,
  wicket_kind TEXT, player_out INTEGER,
  wide INTEGER, noball INTEGER
);
""")
ids = {}
def nid(name):
    if name is None:
        return None
    if name not in ids:
        cur = db.execute("INSERT INTO names(name) VALUES (?)", (name,))
        ids[name] = cur.lastrowid
    return ids[name]

nmatches = 0
zips = [src] if src.endswith(".zip") else sorted(glob.glob(os.path.join(src, "*.zip")))
for zf in zips:
    zip_league = os.path.basename(zf).split("_")[0]
    z = zipfile.ZipFile(zf)
    for fn in z.namelist():
        if not fn.endswith(".json"):
            continue
        try:
            m = json.loads(z.read(fn))
        except Exception:
            continue
        info = m.get("info", {})
        overs = info.get("overs") or 0  # 0 = multi-day (Test/first-class)
        mtype = (info.get("match_type") or "").lower()
        innings = m.get("innings", [])
        if not innings:
            continue
        event_name = ((info.get("event", {}) or {}).get("name") or "").lower()
        league = zip_league
        for code, needle in (("mlc", "major league"), ("ipl", "indian premier"),
                             ("bbl", "big bash"), ("psl", "pakistan super"),
                             ("cpl", "caribbean premier"), ("sa20", "sa20"),
                             ("hundred", "hundred"), ("lpl", "lanka premier"),
                             ("wpl", "women's premier"), ("gsl", "global super")):
            if needle in event_name:
                league = code
                break
        else:
            if zip_league.endswith(".zip") or zip_league in ("all", "recently"):
                league = mtype or "other"
        mid = os.path.basename(fn)[:-5]
        outcome = info.get("outcome", {})
        teams = [inn.get("team") for inn in innings[:2]]
        while len(teams) < 2:
            teams.append(None)
        db.execute("INSERT OR REPLACE INTO matches VALUES (?,?,?,?,?,?,?,?,?,?)", (
            mid, min(info.get("dates", ["?"])), league, overs,
            nid(teams[0]), nid(teams[1]),
            (info.get("venue") or "").split(",")[0].strip(),
            (info.get("event", {}) or {}).get("name", ""),
            nid(outcome.get("winner")),
            outcome.get("result") or ("won" if outcome.get("winner") else "?"),
        ))
        rows = []
        for i, inn in enumerate(innings):
            for ov in inn.get("overs", []):
                for bi, d in enumerate(ov.get("deliveries", [])):
                    w = (d.get("wickets") or [{}])[0]
                    extras = d.get("extras") or {}
                    rows.append((mid, i + 1, ov.get("over", 0) + 1, bi + 1,
                                 nid(d.get("batter")), nid(d.get("bowler")),
                                 d.get("runs", {}).get("batter", 0),
                                 d.get("runs", {}).get("extras", 0),
                                 w.get("kind", ""), nid(w.get("player_out")),
                                 1 if extras.get("wides", 0) > 0 else 0,
                                 1 if extras.get("noballs", 0) > 0 else 0))
        db.executemany("INSERT INTO deliveries VALUES (?,?,?,?,?,?,?,?,?,?,?,?)", rows)
        nmatches += 1
db.executescript("""
CREATE INDEX idx_del_match ON deliveries(match_id, innings, over);
CREATE INDEX idx_del_batter ON deliveries(batter);
CREATE INDEX idx_del_bowler ON deliveries(bowler);
CREATE INDEX idx_match_teams ON matches(team1, team2, date);
CREATE INDEX idx_match_league ON matches(league, date);
CREATE INDEX idx_names ON names(name);
""")
db.commit()
db.execute("VACUUM")
db.close()
print(f"{nmatches:,} matches -> {out} ({os.path.getsize(out)/1e6:.0f}MB)", file=sys.stderr)
