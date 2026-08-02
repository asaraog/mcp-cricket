# Contributing

Bug reports and pull requests are welcome.

## Running the tests

```bash
go test ./...
```

Archive-backed tests skip automatically when no database is present, so the
suite runs without downloading anything.

## Rebuilding the archive

The database is generated from public [Cricsheet](https://cricsheet.org) data:

```bash
curl -O https://cricsheet.org/downloads/all_json.zip
python3 scripts/histgen.py all_json.zip history.db
```

## Rebuilding the win model

Model coefficients are fitted offline and embedded in `internal/explainer`.
The parity test pins Go's output to the Python fit, so changing coefficients
means updating both — and the expectations must be regenerated from the fit,
not copied from Go's output, or the test stops checking anything.
