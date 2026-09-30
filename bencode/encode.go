package bencode

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
)

// Marshal returns the canonical bencode encoding of v: dict keys sorted,
// no leading zeros. Supported types are strings, ints, slices and
// string-keyed maps of those. Bencode has no nil, booleans or
// floats, so those are errors rather than being guessed at.
func Marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	if err := encode(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encode(buf *bytes.Buffer, v any) error {
	switch x := v.(type) {
	case string:
		writeString(buf, x)
	case int:
		writeInt(buf, int64(x))
	case int64:
		writeInt(buf, x)
	case []string:
		buf.WriteByte('l')
		for _, s := range x {
			writeString(buf, s)
		}
		buf.WriteByte('e')
	case []any:
		buf.WriteByte('l')
		for _, item := range x {
			if err := encode(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte('e')
	case map[string]string:
		m := make(map[string]any, len(x))
		for k, s := range x {
			m[k] = s
		}
		return encode(buf, m)
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('d')
		for _, k := range keys {
			writeString(buf, k)
			if err := encode(buf, x[k]); err != nil {
				return fmt.Errorf("key %q: %w", k, err)
			}
		}
		buf.WriteByte('e')
	case nil:
		return fmt.Errorf("bencode: can't encode nil")
	default:
		return fmt.Errorf("bencode: can't encode %T", v)
	}
	return nil
}

func writeString(buf *bytes.Buffer, s string) {
	buf.WriteString(strconv.Itoa(len(s)))
	buf.WriteByte(':')
	buf.WriteString(s)
}

func writeInt(buf *bytes.Buffer, n int64) {
	buf.WriteByte('i')
	buf.WriteString(strconv.FormatInt(n, 10))
	buf.WriteByte('e')
}
