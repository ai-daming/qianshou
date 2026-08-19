// Package strictjson rejects JSON ambiguity before ordinary decoding.
package strictjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func Decode(data []byte, target any, disallowUnknownFields bool) error {
	if err := Validate(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if disallowUnknownFields {
		dec.DisallowUnknownFields()
	}
	if err := dec.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("more than one JSON value")
		}
		return err
	}
	return nil
}

func Validate(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]string{}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("object key is not a string")
				}
				folded := strings.ToLower(key)
				if previous, exists := seen[folded]; exists {
					return fmt.Errorf("duplicate keys %q and %q", previous, key)
				}
				seen[folded] = key
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		case '[':
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		}
		return nil
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("more than one JSON value")
	}
	return nil
}
