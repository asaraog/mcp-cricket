package mcp

// cricket_minor_league_info in the local build.
//
// The hosted server answers questions about Minor League Cricket's past:
// seasons, teams, grounds and player records. That data is not published
// with this repository, so the local build cannot answer them. The tool is
// still listed here, under the same name and with the same arguments, so a
// client moving between the hosted server and this binary sees one tool
// set, and an assistant that reaches for it is told where the data is
// instead of finding no tool at all.

// hostedURL is the hosted MCP endpoint, the one the README leads with.
const hostedURL = "https://cricketfornoobs.com/mcp"

// minorLeagueInfoHosted is the local build's whole answer. One line, the
// same for every call: nothing here depends on the arguments.
const minorLeagueInfoHosted = "Minor League history, teams, grounds and player records are served by the hosted server at " + hostedURL +
	", not by this local build; add that URL as a connector to ask about them. Live Minor League scores and win chances are here, in cricket_minor_league."

func minorLeagueInfoTool(_ map[string]any) (string, error) {
	return minorLeagueInfoHosted, nil
}
