#!/usr/bin/env python3

import re
import sys
from pathlib import Path

KEY_COMMANDS = {
    "Enter": "Enter",
    "Tab": "Tab",
    "Backspace": "Backspace",
    "Delete": "Delete",
    "Insert": "Insert",
    "Up": "↑ Up",
    "Down": "↓ Down",
    "Left": "← Left",
    "Right": "→ Right",
    "Home": "Home",
    "End": "End",
    "PageUp": "PgUp",
    "PageDown": "PgDn",
    "Escape": "Esc",
    "Esc": "Esc",
    "Space": "Space",
}

MODIFIER_PATTERN = re.compile(
    r"^(Ctrl|Control|Alt|Shift|Cmd|Meta)\+([A-Za-z0-9]+)$",
    re.IGNORECASE,
)

SLEEP_RE = re.compile(r"^Sleep\s+(.+?)\s*$", re.IGNORECASE)

DEFAULT_KEY_DURATION = 0.8
FONT_SIZE = 28
X_POS = "w-tw-40"
Y_POS = "h-th-30"
BOX_BORDER = 12
FONT_COLOR = "white"
BOX_COLOR = "black@0.65"


def parse_duration(text: str) -> float:
    s = text.strip().lower()

    if s.endswith("ms"):
        return float(s[:-2]) / 1000.0
    if s.endswith("s"):
        return float(s[:-1])
    if s.endswith("m"):
        return float(s[:-1]) * 60.0

    return float(s)


def normalize_label(command: str):
    cmd = command.strip()

    if cmd in KEY_COMMANDS:
        return KEY_COMMANDS[cmd]

    m = MODIFIER_PATTERN.match(cmd)
    if m:
        mod = m.group(1)
        key = m.group(2)

        mod_map = {
            "control": "Ctrl",
            "ctrl": "Ctrl",
            "alt": "Alt",
            "shift": "Shift",
            "cmd": "Cmd",
            "meta": "Meta",
        }
        mod_norm = mod_map.get(mod.lower(), mod)
        key_norm = key.upper() if len(key) == 1 else key
        return f"{mod_norm}+{key_norm}"

    if re.match(r"^F\d{1,2}$", cmd, re.IGNORECASE):
        return cmd.upper()

    return None


def ffmpeg_escape(text: str) -> str:
    return (
        text.replace("\\", r"\\")
            .replace(":", r"\:")
            .replace("'", r"\'")
            .replace(",", r"\,")
            .replace("[", r"\[")
            .replace("]", r"\]")
            .replace("%", r"\%")
    )


def collect_key_events(lines):
    t = 0.0
    events = []

    for raw_line in lines:
        line = raw_line.strip()

        if not line or line.startswith("#"):
            continue

        m = SLEEP_RE.match(line)
        if m:
            t += parse_duration(m.group(1))
            continue

        if line.startswith(("Type ", "Set ", "Output ", "Require ", "Source ", "Hide", "Show")):
            continue

        label = normalize_label(line)
        if label is not None:
            events.append((t, label))

    return events


def build_drawtext_filters(events):
    filters = []

    for start, label in events:
        end = start + DEFAULT_KEY_DURATION
        escaped = ffmpeg_escape(f"[{label}]")

        draw = (
            "drawtext="
            f"text='{escaped}':"
            f"fontcolor={FONT_COLOR}:"
            f"fontsize={FONT_SIZE}:"
            f"x={X_POS}:"
            f"y={Y_POS}:"
            "box=1:"
            f"boxcolor={BOX_COLOR}:"
            f"boxborderw={BOX_BORDER}:"
            f"enable='between(t,{start:.3f},{end:.3f})'"
        )
        filters.append(draw)

    return ",\n".join(filters)


def main():
    if len(sys.argv) not in (2, 3):
        print("Usage: vhs_keys_to_ffmpeg.py input.tape [output.ffmpeg]", file=sys.stderr)
        sys.exit(1)

    input_path = Path(sys.argv[1])
    if len(sys.argv) == 3:
        output_path = Path(sys.argv[2])
    else:
        output_path = input_path.with_suffix(".ffmpeg")

    lines = input_path.read_text(encoding="utf-8").splitlines()
    events = collect_key_events(lines)
    filter_text = build_drawtext_filters(events)
    output_path.write_text(filter_text + "\n", encoding="utf-8")

    print(f"Wrote {output_path}")
    print(f"Detected {len(events)} key events")


if __name__ == "__main__":
    main()

