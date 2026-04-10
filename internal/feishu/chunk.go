package feishu

import "fmt"

// TruncateMessage 截断消息到指定长度
func TruncateMessage(message string, limit int) string {
	runes := []rune(message)
	if len(runes) > limit {
		return string(runes[:limit-3]) + "..."
	}
	return message
}

// ChunkMessage 将长消息按限制分片，每片附带 [n/m] 后缀
func ChunkMessage(message string, limit int) []string {
	runes := []rune(message)
	if len(runes) == 0 {
		return []string{""}
	}

	total := chunkCount(runes, limit)
	if total <= 1 {
		return []string{message}
	}

	var chunks []string
	start := 0
	for i := 1; i <= total && start < len(runes); i++ {
		suffix := fmt.Sprintf("\n\n[%d/%d]", i, total)
		payloadLimit := limit - len([]rune(suffix))
		if payloadLimit <= 0 {
			payloadLimit = 1
		}

		end := start + payloadLimit
		if end > len(runes) {
			end = len(runes)
		}

		chunk := string(runes[start:end]) + suffix
		chunks = append(chunks, chunk)
		start = end
	}

	return chunks
}

func chunkCount(runes []rune, limit int) int {
	length := len(runes)
	total := 1

	for {
		suffix := chunkSuffix(total, total)
		payloadLimit := limit - len([]rune(suffix))
		if payloadLimit <= 0 {
			payloadLimit = 1
		}
		needed := (length + payloadLimit - 1) / payloadLimit
		if needed == total {
			return total
		}
		total = needed
	}
}

func chunkSuffix(index, total int) string {
	if total <= 1 {
		return ""
	}
	return fmt.Sprintf("\n\n[%d/%d]", index, total)
}
