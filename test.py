#!/usr/bin/env python3

import re
import sys
from pathlib import Path

KEY_COMMANDS = {
    "Tab": "Tab",
    "Backspace": "Backspace",
    "Delete": "Delete",
    "Insert": "Insert",
    "Up": "↑",
    "Down": "↓",
    "Left": "←",
    "Right": "→",
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
TYPE_RE = re.compile(r'^Type\s+"(.*)"\s*$')

DEFAULT_DURATION = 0.9
PLAYRES_X = 1920
PLAYRES_Y = 1080

MARGIN_L = 40
MARGIN_R = 40
MARGIN_V = 36

FONT_NAME = "Menlo"
FONT_SIZE = 42

INCLUDE_SINGLE_NON_ALPHA = True
INCLUDE_SINGLE_ALPHA = False


def parse_duration(text: str) -> float:
    s = text.strip().lower()
    if s.endswith("ms"):
        return float(s[:-2]) / 1000.0
    if s.endswith("s"):
        return float(s[:-1])
    if s.endswith("m"):
        return float(s[:-1]) * 60.0
    return float(s)


def normalize_key_command(command: str):
    cmd = command.strip()

    if cmd == "Enter":
        return None

    if MODIFIER_PATTERN.match(cmd):
        return None

    if cmd in KEY_COMMANDS:
        return KEY_COMMANDS[cmd]

    if re.match(r"^F\d{1,2}$", cmd, re.IGNORECASE):
        return cmd.upper()

    return None


def decode_vhs_string(s: str) -> str:
    s = s.replace(r'\"', '"')
    s = s.replace(r"\\", "\\")
    return s


def extract_type_label(line: str):
    m = TYPE_RE.match(line.strip())
    if not m:
        return None

    text = decode_vhs_string(m.group(1))
    if len(text) != 1:
        return None

    ch = text[0]

    if ch.isalpha():
        if INCLUDE_SINGLE_ALPHA:
            return ch
        return None

    if INCLUDE_SINGLE_NON_ALPHA:
        return ch

    return None


def ass_escape(text: str) -> str:
    return text.replace("\\", r"\\").replace("{", r"\{").replace("}", r"\}")


def format_ass_time(seconds: float) -> str:
    if seconds < 0:
        seconds = 0
    total_cs = round(seconds * 100)
    h = total_cs // 360000
    total_cs %= 360000
    m = total_cs // 6000
    total_cs %= 6000
    s = total_cs // 100
    cs = total_cs % 100
    return f"{h}:{m:02d}:{s:02d}.{cs:02d}"


def collect_events(lines):
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

        type_label = extract_type_label(line)
        if type_label is not None:
            events.append((t, type_label))
            continue

        if line.startswith(("Type ", "Set ", "Output ", "Require ", "Source ", "Hide", "Show")):
            continue

        key_label = normalize_key_command(line)
        if key_label is not None:
            events.append((t, key_label))

    return events


def squash_events(events, duration=DEFAULT_DURATION):
    if not events:
        return []

    out = []
    for i, (start, label) in enumerate(events):
        end = start + duration
        if i + 1 < len(events):
            next_start = events[i + 1][0]
            end = min(end, next_start)
        if end <= start:
            end = start + 0.05
        out.append((start, end, label))
    return out


def build_ass(events):
    header = f"""[Script Info]
ScriptType: v4.00+
PlayResX: {PLAYRES_X}
PlayResY: {PLAYRES_Y}
ScaledBorderAndShadow: yes

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: KeyBadge,{FONT_NAME},{FONT_SIZE},&H00FFFFFF,&H000000FF,&H00000000,&H66000000,1,0,0,0,100,100,0,0,1,3,0,2,{MARGIN_L},{MARGIN_R},{MARGIN_V},1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
"""

    dialogue_lines = []
    for start, end, label in squash_events(events):
        text = ass_escape(label)
        dialogue_lines.append(
            f"Dialogue: 0,{format_ass_time(start)},{format_ass_time(end)},KeyBadge,,0,0,0,,{text}"
        )

    return header + "\n".join(dialogue_lines) + "\n"


def main():
    if len(sys.argv) not in (2, 3):
        print("Usage: vhs_to_ass.py input.tape [output.ass]", file=sys.stderr)
        sys.exit(1)

    input_path = Path(sys.argv[1])
    if len(sys.argv) == 3:
        output_path = Path(sys.argv[2])
    else:
        output_path = input_path.with_suffix(".ass")

    lines = input_path.read_text(encoding="utf-8").splitlines()
    events = collect_events(lines)
    ass_text = build_ass(events)
    output_path.write_text(ass_text, encoding="utf-8")

    print(f"Wrote {output_path}")
    print(f"Detected {len(events)} events")


if __name__ == "__main__":
    main()

