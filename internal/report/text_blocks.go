package report

import "strings"

// reportTextBlock contains untrusted text, never pre-rendered or trusted HTML.
// The report template must escape Text in both paragraph and code contexts.
type reportTextBlock struct {
	Code bool
	Text string
}

// reportTextBlocks recognizes only paired triple-backtick fences on their own
// lines. It does not render Markdown or HTML. Payloads retain their original
// whitespace; malformed or unclosed fences remain literal plain text.
func reportTextBlocks(value string) []reportTextBlock {
	blocks := make([]reportTextBlock, 0)
	plainStart := 0
	for lineStart := 0; lineStart < len(value); {
		lineEnd := reportLineEnd(value, lineStart)
		if _, ok := reportFenceLine(value[lineStart:lineEnd]); !ok {
			lineStart = lineEnd
			continue
		}

		contentStart := lineEnd
		closeStart, closeEnd := -1, -1
		for nextStart := contentStart; nextStart < len(value); {
			nextEnd := reportLineEnd(value, nextStart)
			if info, ok := reportFenceLine(value[nextStart:nextEnd]); ok && info == "" {
				closeStart, closeEnd = nextStart, nextEnd
				break
			}
			nextStart = nextEnd
		}
		if closeStart < 0 {
			// Do not discard the opening fence or reinterpret an unfinished code
			// payload as additional Markdown. Keep the remainder verbatim.
			break
		}
		if plainStart < lineStart {
			blocks = append(blocks, reportTextBlock{Text: value[plainStart:lineStart]})
		}
		blocks = append(blocks, reportTextBlock{Code: true, Text: value[contentStart:closeStart]})
		plainStart, lineStart = closeEnd, closeEnd
	}
	if plainStart < len(value) {
		blocks = append(blocks, reportTextBlock{Text: value[plainStart:]})
	}
	return blocks
}

func reportLineEnd(value string, start int) int {
	if offset := strings.IndexByte(value[start:], '\n'); offset >= 0 {
		return start + offset + 1
	}
	return len(value)
}

func reportFenceLine(line string) (string, bool) {
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	indent := len(line) - len(strings.TrimLeft(line, " "))
	if indent > 3 {
		return "", false
	}
	line = line[indent:]
	if !strings.HasPrefix(line, "```") || strings.HasPrefix(line, "````") {
		return "", false
	}
	info := strings.TrimSpace(line[3:])
	if strings.Contains(info, "`") {
		return "", false
	}
	return info, true
}
