package daemon

import (
	"testing"

	"github.com/matthewlu070111/smart-srun/core/internal/application"
	"github.com/matthewlu070111/smart-srun/core/internal/config"
	"github.com/matthewlu070111/smart-srun/core/internal/domain"
)

// R08 -- everything that touches the radio serialises on one key.
//
// A hotspot switch carries a HotspotID and usually no AccountID, so resolving
// the account first sent it to "account:" while a campus wireless switch went
// to "wireless": two keys for one radio, which lets the coordinator dispatch
// both at once. Nothing has been seen corrupting a real configuration because
// the wireless transaction does not exist yet -- which is exactly why this has
// to be right before M10 attaches the side effects.
//
// The scheduling key is not a substitute for the global wireless transaction
// lock spec 04 requires. It keeps this process from dispatching two wireless
// actions at once; the lock protects the radio from everything else.
func TestEverythingThatTouchesTheRadioSharesOneSchedulingKey(t *testing.T) {
	service := &Daemon{config: wirelessRepository(t)}

	campus := service.lineOf(application.Request{
		Kind: application.KindSwitchCampus, AccountID: "wifi"})
	hotspot := service.lineOf(application.Request{
		Kind: application.KindSwitchHotspot, HotspotID: "h1"})

	if campus != hotspot {
		t.Fatalf("campus wireless = %q, hotspot = %q; one radio, two keys",
			campus, hotspot)
	}
	if campus != wirelessLine {
		t.Errorf("key = %q, want the wireless line", campus)
	}

	// A hotspot switch that does name an account still lands on the radio, so
	// the key cannot be talked out of it by filling in a field.
	withAccount := service.lineOf(application.Request{
		Kind: application.KindSwitchHotspot, HotspotID: "h1", AccountID: "wired"})
	if withAccount != wirelessLine {
		t.Errorf("key = %q for a hotspot switch naming a wired account, want the wireless line",
			withAccount)
	}
}

// And wired accounts keep their own keys, because they share no resource with
// the radio and serialising them onto it would throw away the parallelism that
// lets four lines authenticate at once.
func TestWiredAccountsAreNotSerialisedOntoTheRadio(t *testing.T) {
	service := &Daemon{config: wirelessRepository(t)}

	wired := service.lineOf(application.Request{
		Kind: application.KindLogin, AccountID: "wired"})
	if wired == wirelessLine {
		t.Fatalf("a wired login took the wireless key")
	}
	if wired != "iface:wan" {
		t.Errorf("key = %q, want the interface it authenticates through", wired)
	}

	// Two wired accounts on different interfaces stay independent.
	other := service.lineOf(application.Request{
		Kind: application.KindLogin, AccountID: "wired2"})
	if other == wired {
		t.Errorf("two interfaces shared the key %q", wired)
	}
}

// wirelessRepository is a repository holding one wireless account, two wired
// ones and a hotspot.
func wirelessRepository(t *testing.T) *config.Repository {
	t.Helper()
	paths := tempPaths(t)
	repository, err := config.Open(paths.ConfigFile())
	if err != nil {
		t.Fatalf("open config: %v", err)
	}
	if _, err := repository.Update(repository.Revision(), func(cfg *domain.Config) error {
		cfg.STAIface = "wwan"
		cfg.CampusAccounts = []domain.CampusAccount{
			{
				ID: "wifi", Label: "无线", UserID: "a", Password: "p",
				AccessMode: domain.AccessModeWiFi, SSID: "campus",
				Encryption: "none", APSelection: domain.APSelectionAuto,
				BaseURL: "http://192.0.2.1", ACID: "1",
			},
			{
				ID: "wired", Label: "有线", UserID: "b", Password: "p",
				AccessMode: domain.AccessModeWired, WiredIface: "wan",
				BaseURL: "http://192.0.2.1", ACID: "1",
			},
			{
				ID: "wired2", Label: "有线二", UserID: "c", Password: "p",
				AccessMode: domain.AccessModeWired, WiredIface: "wan2",
				BaseURL: "http://192.0.2.1", ACID: "1",
			},
		}
		cfg.HotspotProfiles = []domain.HotspotProfile{
			{ID: "h1", Label: "手机热点", SSID: "phone", Encryption: "psk2",
				Key: "letmein1", Radio: "radio0"},
		}
		return nil
	}); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return repository
}
