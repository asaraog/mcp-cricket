"""Launcher for the cricket MCP server.

The server itself is a single static Go binary bundled in this wheel, so
`pip install cricket-mcp` (or `uvx cricket-mcp`) needs no toolchain and no
runtime dependencies. This module just finds the binary and hands over the
process, passing stdio through untouched — MCP speaks JSON-RPC on stdin
and stdout, so nothing may be printed here.
"""
import os
import sys
from pathlib import Path


def _binary() -> Path:
    name = "cricket-mcp.exe" if sys.platform.startswith("win") else "cricket-mcp"
    path = Path(__file__).parent / "bin" / name
    if not path.exists():
        raise SystemExit(
            "cricket-mcp: bundled binary missing for this platform. "
            "Download it from https://github.com/asaraog/cricket-mcp/releases"
        )
    return path


def main() -> None:
    exe = _binary()
    os.chmod(exe, 0o755)
    args = [str(exe), *sys.argv[1:]]
    if sys.platform.startswith("win"):
        import subprocess
        raise SystemExit(subprocess.call(args))
    os.execv(str(exe), args)  # replace this process; stdio passes straight through
