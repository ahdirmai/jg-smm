package service

import (
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

func TestPostExternalIDFromURL(t *testing.T) {
	cases := []struct {
		p    domain.Platform
		url  string
		want string
	}{
		{domain.PlatformInstagram, "https://www.instagram.com/p/DdaJ8Y8gp3u/?img_index=1", "DdaJ8Y8gp3u"},
		{domain.PlatformInstagram, "https://www.instagram.com/p/DdaJ8Y8gp3u/", "DdaJ8Y8gp3u"},
		{domain.PlatformInstagram, "https://www.instagram.com/reel/ABC123/", "ABC123"},
		{domain.PlatformInstagram, "https://instagram.com/tv/XYZ/#frag", "XYZ"},
		{domain.PlatformInstagram, "https://www.instagram.com/someuser/", ""},
		{domain.PlatformThreads, "https://www.threads.com/@user/post/Czzz99", "Czzz99"},
		{domain.PlatformThreads, "https://www.threads.net/@user/post/Czzz99?x=1", "Czzz99"},
	}
	for _, c := range cases {
		if got := postExternalIDFromURL(c.p, c.url); got != c.want {
			t.Errorf("postExternalIDFromURL(%s, %q) = %q, want %q", c.p, c.url, got, c.want)
		}
	}
}
