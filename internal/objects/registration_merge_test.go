package objects

import (
	"testing"
	"time"
)

func TestMergeRegistrationMetadata(t *testing.T) {
	oldTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Hour)
	tests := []struct {
		name string
		in   RegistrationMergeInput
		want RegistrationMergeResult
	}{
		{
			name: "replacement updates metadata and preserves old name",
			in: RegistrationMergeInput{
				ExistingName: "old.txt", ExistingVersion: "1", ExistingDescription: "old", ExistingSize: 7, ExistingUpdated: oldTime,
				IncomingName: `nested\\new.txt`, IncomingVersion: "2", IncomingDescription: "new", IncomingSize: 9, IncomingUpdated: newTime,
				CurrentResources: []string{"/organization/o/project/p"}, IncomingResources: []string{"/organization/o/project/p"},
			},
			want: RegistrationMergeResult{Name: "new.txt", Version: "2", Description: "new", Size: 7, Updated: newTime, NameAlias: "old.txt"},
		},
		{
			name: "non replacement keeps metadata and aliases incoming name",
			in: RegistrationMergeInput{
				ExistingName: "old.txt", ExistingVersion: "1", ExistingDescription: "old", ExistingSize: 7, ExistingUpdated: oldTime,
				IncomingName: "/tmp/new.txt", IncomingVersion: "2", IncomingDescription: "new", IncomingSize: 9, IncomingUpdated: newTime,
				CurrentResources: []string{"/organization/o/project/p", "/organization/o/project/q"}, IncomingResources: []string{"/organization/o/project/p"},
			},
			want: RegistrationMergeResult{Name: "old.txt", Version: "1", Description: "old", Size: 7, Updated: newTime, NameAlias: "new.txt"},
		},
		{
			name: "one nonoverlapping resource does not replace",
			in: RegistrationMergeInput{
				ExistingName: "old.txt", ExistingVersion: "1", ExistingDescription: "old", ExistingSize: 7, ExistingUpdated: oldTime,
				IncomingName: "new.txt", IncomingVersion: "2", IncomingDescription: "new", IncomingSize: 9, IncomingUpdated: newTime,
				CurrentResources: []string{"/organization/o/project/p"}, IncomingResources: []string{"/organization/o/project/q"},
			},
			want: RegistrationMergeResult{Name: "old.txt", Version: "1", Description: "old", Size: 7, Updated: newTime, NameAlias: "new.txt"},
		},
		{
			name: "empty stored fields are filled",
			in: RegistrationMergeInput{
				ExistingUpdated: oldTime,
				IncomingName:    "dir/new.txt", IncomingVersion: "2", IncomingDescription: "new", IncomingSize: 9, IncomingUpdated: newTime,
			},
			want: RegistrationMergeResult{Name: "new.txt", Version: "2", Description: "new", Size: 9, Updated: newTime},
		},
		{
			name: "blank incoming fields do not erase during replacement",
			in: RegistrationMergeInput{
				ExistingName: "old.txt", ExistingVersion: "1", ExistingDescription: "old", ExistingSize: 7, ExistingUpdated: oldTime,
				IncomingUpdated: newTime, CurrentResources: []string{"/organization/o/project/p"}, IncomingResources: []string{"/organization/o/project/p"},
			},
			want: RegistrationMergeResult{Name: "old.txt", Version: "1", Description: "old", Size: 7, Updated: newTime},
		},
		{
			name: "equal names do not create alias",
			in: RegistrationMergeInput{
				ExistingName: "same.txt", ExistingUpdated: oldTime, IncomingName: "/tmp/same.txt", IncomingUpdated: newTime,
				CurrentResources: []string{"/organization/o/project/p"}, IncomingResources: []string{"/organization/o/project/p"},
			},
			want: RegistrationMergeResult{Name: "same.txt", Updated: newTime},
		},
		{
			name: "blank fields do not erase and zero size does not replace",
			in: RegistrationMergeInput{
				ExistingName: "old.txt", ExistingVersion: "1", ExistingDescription: "old", ExistingSize: 7, ExistingUpdated: newTime,
				IncomingName: "", IncomingVersion: "", IncomingDescription: "", IncomingSize: 0, IncomingUpdated: oldTime,
			},
			want: RegistrationMergeResult{Name: "old.txt", Version: "1", Description: "old", Size: 7, Updated: newTime},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MergeRegistrationMetadata(tt.in); got != tt.want {
				t.Fatalf("MergeRegistrationMetadata() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
