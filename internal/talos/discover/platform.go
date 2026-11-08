package discover

import "regexp"

var tagRe = regexp.MustCompile(`^v([0-9]{1,4})\.([0-9]{1,4})(\.|-|$)`)

// contractOf is the contract minor of a Talos version tag (v1.14.5 gives v1.14), "" when the tag
// is not one.
func contractOf(tag string) string {
	m := tagRe.FindStringSubmatch(tag)
	if m == nil {
		return ""
	}
	return "v" + m[1] + "." + m[2]
}

// platform maps a node's Talos platform name to the three values a machine records: metal and
// container are their own, every other Talos platform is a cloud. A node that reports none maps to
// "", which is not a value a machine can be recorded with.
func platform(name string) string {
	switch name {
	case "":
		return ""
	case "metal", "container":
		return name
	}
	return "cloud"
}
