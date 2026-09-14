package domain

// Platform identifies a social platform. MVP active set is Instagram + Threads.
// Keep in sync with packages/shared/src/constants.ts.
type Platform string

const (
	PlatformInstagram Platform = "instagram"
	PlatformThreads   Platform = "threads"
	PlatformFacebook  Platform = "facebook"
	PlatformLinkedIn  Platform = "linkedin"
	PlatformX         Platform = "x"
	PlatformYouTube   Platform = "youtube"
	PlatformTikTok    Platform = "tiktok"
)

// AllPlatforms lists every supported platform in a stable order.
var AllPlatforms = []Platform{
	PlatformInstagram,
	PlatformThreads,
	PlatformFacebook,
	PlatformLinkedIn,
	PlatformX,
	PlatformYouTube,
	PlatformTikTok,
}

// MVPPlatforms are the platforms enabled in the MVP rollout.
var MVPPlatforms = []Platform{PlatformInstagram, PlatformThreads}
