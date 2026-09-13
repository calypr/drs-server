package drs

import "encoding/json"

// MarshalJSON keeps object identity available when a legacy timestamp cannot
// be represented by encoding/json. The normal representation remains the
// generated DrsObject shape.
func (value DrsObject) MarshalJSON() ([]byte, error) {
	type generatedDrsObject DrsObject
	encoded, err := json.Marshal(generatedDrsObject(value))
	if err == nil {
		return encoded, nil
	}
	did := value.Id
	if value.Did != nil {
		did = *value.Did
	}
	return json.Marshal(struct {
		ID      string `json:"id,omitempty"`
		DID     string `json:"did,omitempty"`
		SelfURI string `json:"self_uri"`
	}{ID: value.Id, DID: did, SelfURI: value.SelfUri})
}
