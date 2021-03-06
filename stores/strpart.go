package stores

type StrPart []int

func (part StrPart) Split(str string) (parts []string) {
	parts = make([]string, len(part))
	nchars, offset := len(str), 0
	for i, n := range part {
		if left := nchars - offset; n > left {
			n = left
		}
		parts[i] = str[offset : offset+n]
		offset += n
	}
	return
}
