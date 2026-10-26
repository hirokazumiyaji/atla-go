package naming

import "testing"

func TestPascal(t *testing.T) {
	cases := map[string]string{
		"user_id":     "UserID",
		"avatar_url":  "AvatarURL",
		"api_keys":    "APIKeys",
		"http_status": "HTTPStatus",
		"user_ids":    "UserIDs",
		"created_at":  "CreatedAt",
		"userName":    "UserName",
		"json_data":   "JSONData",
		"uuid":        "UUID",
		"utf8_text":   "UTF8Text",
		"HTTPStatus":  "HTTPStatus",
		"userIDs":     "UserIDs",
		"USER_ID":     "UserID",
	}
	for in, want := range cases {
		if got := Pascal(in); got != want {
			t.Errorf("Pascal(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPascalSanitizes(t *testing.T) {
	cases := map[string]string{
		"order-items": "OrderItems",
		"first name":  "FirstName",
		"2fa_codes":   "X2faCodes",
		"in-progress": "InProgress",
		"--":          "",
		"":            "",
		"café_menu":   "CafMenu",
	}
	for in, want := range cases {
		if got := Pascal(in); got != want {
			t.Errorf("Pascal(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSingular(t *testing.T) {
	cases := map[string]string{
		"users":      "user",
		"categories": "category",
		"addresses":  "address",
		"boxes":      "box",
		"statuses":   "status",
		"people":     "person",
		"children":   "child",
		"movies":     "movie",
		"series":     "series",
		"status":     "status",
		"analysis":   "analysis",
		"data":       "data",
		"class":      "class",
		"keys":       "key",
		"ids":        "id",
		"s":          "s",
		"":           "",
	}
	for in, want := range cases {
		if got := Singular(in); got != want {
			t.Errorf("Singular(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestModelName(t *testing.T) {
	cases := map[string]string{
		"users":     "User",
		"api_keys":  "APIKey",
		"user_ids":  "UserID",
		"dns":       "DNS",
		"https":     "HTTPS",
		"user_dns":  "UserDNS",
		"acl":       "ACL",
		"uuids":     "UUID",
		"2fa_codes": "X2faCode",
	}
	for in, want := range cases {
		if got := modelName(in); got != want {
			t.Errorf("modelName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPackageName(t *testing.T) {
	cases := []struct{ model, root, want string }{
		{"UserProfile", "dbgen", "userprofile"},
		{"Type", "dbgen", "typetbl"},
		{"Enum", "dbgen", "enumtbl"},
		{"String", "dbgen", "stringtbl"},
		{"Dbgen", "dbgen", "dbgentbl"},
		{"Dialect", "dbgen", "dialecttbl"},
		{"Atla", "dbgen", "atlatbl"},
		{"Context", "dbgen", "contexttbl"},
		{"Any", "dbgen", "anytbl"},
		{"Main", "dbgen", "maintbl"},
		{"Internal", "dbgen", "internaltbl"},
		{"Vendor", "dbgen", "vendortbl"},
		{"User", "dbgen", "user"},
	}
	for _, c := range cases {
		if got := PackageName(c.model, c.root); got != c.want {
			t.Errorf("PackageName(%q, %q) = %q, want %q", c.model, c.root, got, c.want)
		}
	}
}
