package cache_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/inconshreveable/log15/v3"
	"github.com/kalbasit/ncps/pkg/cache"
	"github.com/kalbasit/ncps/pkg/cache/upstream"
	"github.com/kalbasit/ncps/pkg/server"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/narinfo/signature"
)

//nolint:gochecknoglobals
var logger = log15.New()

//nolint:gochecknoinits
func init() {
	logger.SetHandler(log15.DiscardHandler())
}

const (
	nixStoreInfo = `StoreDir: /nix/store
WantMassQuery: 1
Priority: 40`

	narInfoHash1 = "7bn85d74qa0127p85rrswfyghxsqmcf7"

	//nolint:lll
	narInfoText1 = `StorePath: /nix/store/7bn85d74qa0127p85rrswfyghxsqmcf7-iputils-20210722
URL: nar/136jk8xlxqzqd16d00dpnnpgffmycwm66zgky6397x75yg7ylz00.nar
Compression: xz
FileHash: sha256:136jk8xlxqzqd16d00dpnnpgffmycwm66zgky6397x75yg7ylz00
FileSize: 132228
NarHash: sha256:1rzb80kz42wy067pp160rridw41dnc09d2a3cqj2wdg6ylklhxkh
NarSize: 534160
References: 7bn85d74qa0127p85rrswfyghxsqmcf7-iputils-20210722 892cxk44qxzzlw9h90a781zpy1j7gmmn-libidn2-2.3.2 l25bc19is0s27929kxkfhgdzhc7x9g5m-libcap-2.49-lib rir9pf0kz1mb84x5bd3yr0fx415yy423-glibc-2.33-123
Deriver: 9fs4vq4gdsb8r9ywawb5f6zl40ycp1bh-iputils-20210722.drv
Sig: cache.nixos.org-1:WzhkqDdkgPz2qU/0QyEA6wUIm7EMR5MY8nTb5jAmmoh5b80ACIp/+Zpgi5t1KvmO8uG8GVrkPejCxbyQ2gNXDQ==`

	narHash1 = "136jk8xlxqzqd16d00dpnnpgffmycwm66zgky6397x75yg7ylz00"

	narText1 = "Hello, World" // fake nar for above nar info

	narInfoHash2 = "a6hiaxlxf7arxsf59f4rzs7b337z6a11"

	//nolint:lll
	narInfoText2 = `StorePath: /nix/store/a6hiaxlxf7arxsf59f4rzs7b337z6a11-brave-1.73.91
URL: nar/1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps.nar
Compression: xz
FileHash: sha256:1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps
FileSize: 125606280
NarHash: sha256:1q95nilzb512ckljzj7h8bsdnj4qhpvyddp74bhsf8sxb8vjmpsj
NarSize: 388038816
References: 08mdbxgs2m7fz3hx2xrqrbmhldxagpf8-pango-1.54.0-bin 0irlcqx2n3qm6b1pc9rsd2i8qpvcccaj-bash-5.2p37 2542sr1ch7ypz52w423vyn0fhn934rj7-dbus-1.14.10 3c603pkfh9fq13lrm6rdqynb7xxwazv7-libXrandr-1.5.4 3ma7c4syxds2wrp03ax3b7p9fdsh1lwg-nss-3.101.2 4qiqy2z0facj37682n9x0mz0isd9k3qb-libXScrnSaver-1.2.4 4zjxqvvnnisn8nqmibgy40rvm087v85n-cups-2.4.11-lib 566jyp236h118ad005mxqs7sb9ivv4yj-libXrender-0.9.11 5lq743a2jp3v4kgr80y0sw8jr70pl6gj-libXi-1.8.2 7c88jgbh10zvlv91bmhyc40h8l9b3wf5-librsvg-2.58.3 84839y8j0d4l4d3q048v00h20jdk9lh0-wayland-1.23.1 946m0cmd00hcw2s6slzp5qc01krgrzks-dbus-1.14.10-lib 9i9wrwdi1hkcklzya0yhci0d0vkyl79k-alsa-lib-1.2.12 a6hiaxlxf7arxsf59f4rzs7b337z6a11-brave-1.73.91 apmrnyl4b6fj9czqkb8vwpf6cvfvs0ba-glib-2.82.1-bin arrgysrp5ks44qidsf8byjj11pnkaf65-cairo-1.18.2 b4ph9mwdv628db2vqhhhp3azalq6cwbq-libxcb-1.17.0 c1lszwlw825y6vr4qik6dqv2lszqk08r-libdrm-2.4.123-bin ckfsxyla4krxn4zdlmxkp6lk8nsscr5v-libXcursor-1.2.2 dcj4w0a7472g86yf5wqz5vfbd809s4cj-snappy-1.2.1 gsdkqm4qfbzr22m20cns3iilxd55ridd-freetype-2.13.3 hm2rpszabwpvs28jwkaz7pg0dqslm4bd-util-linux-minimal-2.39.4-lib hpd1dss7lmrywlr37vncyzbhzp6rgri5-libXext-1.3.6 i0d0ws5xwix1dvmwp8w1fr4kz61k11x4-libxkbcommon-1.7.0 i66c6m4680fdgfj5fmaclma1bbl9y8i6-systemd-minimal-libs-256.7 ibr7wf806ns8517zggai9yygz07kp85a-libdrm-2.4.123 idrnfiw9r87giqiqwr36xkch4dsr0rdz-pango-1.54.0 j6wpl172pz2323fia7cbjx9lznsc47ri-at-spi2-core-2.54.0 jbck0ahim6cbjjzl1wmrch5inhbhkkra-gtk4-4.16.3 jq1ydifw3ip1vx94dl8w8bgvr07l6s3x-glib-2.82.1 jx4rwrk7cg5v64ygbpyi1v3gfp8c7k9h-fontconfig-2.15.0-bin k48bha2fjqzarg52picsdfwlqx75aqbb-coreutils-9.5 lafwzwzrj53prpzgmmq0aq5spal91q7s-libXdamage-1.1.6 lcq3ibmsb6c2jgqp3yfi1yp773x5wz19-mesa-24.2.6 ldqjvk81fdzygd85505lzc781vssfpdy-libXtst-1.2.5 m761lp9x14i33hb3q7a8wm52rsnk6ab1-expat-2.6.4 mafw0r1djqvl36by8qhz02kmaggh24v8-gdk-pixbuf-2.42.12 mhp5jkwqgyz0s7klvpl23x3axgvn6msm-pipewire-1.2.6 ms2562w6dkqa6xzpaxi4n2ib9kbb3gya-libXcomposite-0.4.6 p5nz6gdblng41fiqqb7z88l119dc9v56-gsettings-desktop-schemas-47.1 pacbfvpzqz2mksby36awvbcn051zcji3-glibc-2.40-36 pg3xw94i23l4dkrxcp7l11a2iydxc1za-dconf-0.40.0-lib q8y8kgidr4mg0q7zr2h1ddpd7ijy3wna-libpulseaudio-17.0 qrj5f1mb08k5jg7y9ikzb1njli0gvmxz-libX11-1.8.10 rdjcf9pxdlcd6ncsiqm97gq4ard6c91f-nspr-4.36 rh6zrnvbzjsfy444w5w2k5sz72cl6f4z-libva-2.22.0 rmfav7fq6rmpv3d785v462shsy1njq6f-fontconfig-2.15.0-lib sawcd2sh4p7kdggj00i67wa59swaid5b-util-linux-minimal-2.39.4-bin vbw9c3pvwli5iidik8dddgz423wr1h18-gtk+3-3.24.43 w3clwb8c7c8vc7ls163p5pjspccgcrhj-cups-2.4.11 w9g781rqrc12870vji13hycwh47lwsgd-qtbase-6.8.0 wfqs8h2mf4ib7f1phpsgv0b20xny9zzl-libXfixes-6.0.1 wjnznkh0x4744mcvhlnr4qnnig15287c-xdg-utils-1.2.1 xghyamhbjj0rfavxj76l52mv7zr7dfiw-krb5-1.21.3-lib xk9b2k475ab7q9xmcqcfb1xcyh9arswj-krb5-1.21.3 y6bcc1vzg315zzzpir608zkhnmvp49kc-zlib-1.3.1 ymhcg6x5jrw3hx8ik1cji6awiybgp9f7-libglvnd-1.7.0 zm54lx2l4jsc0dw1cnmyg8ilcn07v6zb-libxshmfence-1.3.2
Deriver: n85jhy5b8c9l1a4d9pnf3dv8bfvmyzhb-brave-1.73.91.drv
Sig: cache.nixos.org-1:QVc2ad4OI/2G5gVyFsoWYr+AECFDXjwF6o1HOfSbMJhPp5l4iD38WgJhf2w/3kZ/B5ftq6ytDijcAWCSDItJDQ==`

	narHash2 = "1mb5fxh7nzbx1b2q40bgzwjnjh8xqfap9mfnfqxlvvgvdyv8xwps"

	narText2 = "Wave, World" // fake nar for above nar info
)

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("path must be absolute, must exist, and must be a writable directory", func(t *testing.T) {
		t.Parallel()

		t.Run("path is required", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "hello", nil)
			if want, got := cache.ErrPathMustBeAbsolute, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path is not absolute", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "hello", nil)
			if want, got := cache.ErrPathMustBeAbsolute, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path must exist", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "/non-existing", nil)
			if want, got := cache.ErrPathMustExist, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path must be a directory", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "/proc/cpuinfo", nil)
			if want, got := cache.ErrPathMustBeADirectory, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("path must be writable", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", "/root", nil)
			if want, got := cache.ErrPathMustBeWritable, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("valid path must return no error", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", os.TempDir(), nil)
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}
		})

		t.Run("config/, store/nar and store/tmp were created", func(t *testing.T) {
			dir, err := os.MkdirTemp("", "cache-path-")
			if err != nil {
				t.Fatalf("expected no error, got: %q", err)
			}
			defer os.RemoveAll(dir) // clean up

			_, err = cache.New(logger, "cache.example.com", dir, nil)
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}

			dirs := []string{"config", "store", filepath.Join("store", "nar"), filepath.Join("store", "tmp")}

			for _, p := range dirs {
				t.Run("Checking that "+p+" exists", func(t *testing.T) {
					info, err := os.Stat(filepath.Join(dir, p))
					if err != nil {
						t.Fatalf("expected no error, got: %s", err)
					}

					if want, got := true, info.IsDir(); want != got {
						t.Errorf("want %t got %t", want, got)
					}
				})
			}
		})

		t.Run("store/tmp is removed on boot", func(t *testing.T) {
			dir, err := os.MkdirTemp("", "cache-path-")
			if err != nil {
				t.Fatalf("expected no error, got: %q", err)
			}
			defer os.RemoveAll(dir) // clean up

			// create the directory tmp and add a file inside of it
			if err := os.MkdirAll(filepath.Join(dir, "store", "tmp"), 0o700); err != nil {
				t.Fatalf("expected no error but got %s", err)
			}

			f, err := os.CreateTemp(filepath.Join(dir, "store", "tmp"), "hello")
			if err != nil {
				t.Fatalf("expected no error but got %s", err)
			}

			_, err = cache.New(logger, "cache.example.com", dir, nil)
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}

			if _, err := os.Stat(f.Name()); !os.IsNotExist(err) {
				t.Errorf("expected %q to not exist but it does", f.Name())
			}
		})
	})

	t.Run("hostname must be valid with no scheme or path", func(t *testing.T) {
		t.Parallel()

		t.Run("hostname must not be empty", func(t *testing.T) {
			_, err := cache.New(logger, "", os.TempDir(), nil)
			if want, got := cache.ErrHostnameRequired, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("hostname must not contain scheme", func(t *testing.T) {
			_, err := cache.New(logger, "https://cache.example.com", os.TempDir(), nil)
			if want, got := cache.ErrHostnameMustNotContainScheme, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("hostname must not contain a path", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com/path/to", os.TempDir(), nil)
			if want, got := cache.ErrHostnameMustNotContainPath, err; !errors.Is(got, want) {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("valid hostName must return no error", func(t *testing.T) {
			_, err := cache.New(logger, "cache.example.com", os.TempDir(), nil)
			if err != nil {
				t.Errorf("expected no error, got %q", err)
			}
		})
	})
}

func TestPublicKey(t *testing.T) {
	t.Parallel()

	c, err := cache.New(logger, "cache.example.com", "/tmp", nil)
	if err != nil {
		t.Fatalf("error not expected, got an error: %s", err)
	}

	pubKey := c.PublicKey().String()

	t.Run("should return a public key with the correct prefix", func(t *testing.T) {
		t.Parallel()

		if !strings.HasPrefix(pubKey, "cache.example.com:") {
			t.Errorf("public key should start with cache.example.com: but it does not: %s", pubKey)
		}
	})

	t.Run("should return a valid public key", func(t *testing.T) {
		t.Parallel()

		pk, err := signature.ParsePublicKey(pubKey)
		if err != nil {
			t.Fatalf("error is not expected: %s", err)
		}

		if want, got := pubKey, pk.String(); want != got {
			t.Errorf("want %q got %q", want, got)
		}
	})
}

//nolint:paralleltest
func TestGetNarInfo(t *testing.T) {
	ts := startServer(t)
	defer ts.Close()

	tu, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("error not expected, got %s", err)
	}

	dir, err := os.MkdirTemp("", "cache-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	uc, err := upstream.New(logger, tu.Host, []string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="})
	if err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	c, err := cache.New(logger, "cache.example.com", dir, []upstream.Cache{uc})
	if err != nil {
		t.Errorf("expected no error, got %q", err)
	}

	t.Run("narfile does not exist upstream", func(t *testing.T) {
		_, err := c.GetNarInfo(context.Background(), "doesnotexist")
		if want, got := cache.ErrNotFound, err; !errors.Is(got, want) {
			t.Errorf("want %s got %s", want, got)
		}
	})

	t.Run("narfile exists upstream", func(t *testing.T) {
		t.Run("narfile does not exist in storage yet", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(dir, "store", narInfoHash2+".narinfo"))
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})

		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(dir, "store", narHash2+".nar"))
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})

		ni, err := c.GetNarInfo(context.Background(), narInfoHash2)
		if err != nil {
			t.Fatalf("no error expected, got: %s", err)
		}

		t.Run("size is correct", func(t *testing.T) {
			if want, got := uint64(125606280), ni.FileSize; want != got {
				t.Errorf("want %d got %d", want, got)
			}
		})

		t.Run("it should now exist in the store", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(dir, "store", narInfoHash2+".narinfo"))
			if err != nil {
				t.Fatalf("expected no error got %s", err)
			}
		})

		t.Run("it should be signed by our server", func(t *testing.T) {
			var found bool

			var sig signature.Signature
			for _, sig = range ni.Signatures {
				if sig.Name == "cache.example.com" {
					found = true

					break
				}
			}

			if want, got := true, found; want != got {
				t.Errorf("want %t got %t", want, got)
			}

			validSig := signature.VerifyFirst(ni.Fingerprint(), ni.Signatures, []signature.PublicKey{c.PublicKey()})

			if want, got := true, validSig; want != got {
				t.Errorf("want %t got %t", want, got)
			}
		})

		t.Run("it should have also pulled the nar", func(t *testing.T) {
			// Force the other goroutine to run so it actually download the file
			// Try at least 10 times before announcing an error
			var err error

			for i := 0; i < 9; i++ {
				// NOTE: I tried runtime.Gosched() but it makes the test flaky
				time.Sleep(time.Millisecond)

				_, err = os.Stat(filepath.Join(dir, "store", "nar", narHash2+".nar"))
				if err == nil {
					break
				}
			}

			if err != nil {
				t.Errorf("expected no error got %s", err)
			}
		})
	})
}

//nolint:paralleltest
func TestPutNarInfo(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	c, err := cache.New(logger, "cache.example.com", dir, nil)
	if err != nil {
		t.Errorf("expected no error, got %q", err)
	}

	s := server.New(logger, c)

	ts := httptest.NewServer(s)
	defer ts.Close()

	storePath := filepath.Join(dir, "store", narInfoHash1+".narinfo")

	t.Run("narfile does not exist in storage yet", func(t *testing.T) {
		_, err := os.Stat(storePath)
		if err == nil {
			t.Fatal("expected an error but got none")
		}
	})

	t.Run("putNarFile does not return an error", func(t *testing.T) {
		p := ts.URL + "/" + narInfoHash1 + ".narinfo"

		r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(narInfoText1))
		if err != nil {
			t.Fatalf("error Do(r): %s", err)
		}

		resp, err := ts.Client().Do(r)
		if err != nil {
			t.Fatalf("error Do(r): %s", err)
		}

		if want, got := http.StatusNoContent, resp.StatusCode; want != got {
			t.Errorf("want %d got %d", want, got)
		}
	})

	t.Run("narfile does exist in storage", func(t *testing.T) {
		_, err := os.Stat(storePath)
		if err != nil {
			t.Fatalf("expected no error but got: %s", err)
		}
	})

	t.Run("it should be signed by our server", func(t *testing.T) {
		f, err := os.Open(storePath)
		if err != nil {
			t.Fatalf("no error was expected, got: %s", err)
		}

		ni, err := narinfo.Parse(f)
		if err != nil {
			t.Fatalf("no error was expected, got: %s", err)
		}

		var found bool

		var sig signature.Signature
		for _, sig = range ni.Signatures {
			if sig.Name == "cache.example.com" {
				found = true

				break
			}
		}

		if want, got := true, found; want != got {
			t.Errorf("want %t got %t", want, got)
		}

		validSig := signature.VerifyFirst(ni.Fingerprint(), ni.Signatures, []signature.PublicKey{c.PublicKey()})

		if want, got := true, validSig; want != got {
			t.Errorf("want %t got %t", want, got)
		}
	})
}

//nolint:paralleltest
func TestDeleteNarInfo(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	c, err := cache.New(logger, "cache.example.com", dir, nil)
	if err != nil {
		t.Errorf("expected no error, got %q", err)
	}

	s := server.New(logger, c)

	ts := httptest.NewServer(s)
	defer ts.Close()

	storePath := filepath.Join(dir, "store", narInfoHash1+".narinfo")

	t.Run("narinfo does not exist in storage yet", func(t *testing.T) {
		_, err := os.Stat(storePath)
		if err == nil {
			t.Fatal("expected an error but got none")
		}
	})

	f, err := os.Create(storePath)
	if err != nil {
		t.Fatalf("expecting no error got %s", err)
	}

	if _, err := f.WriteString(narInfoText1); err != nil {
		t.Fatalf("expecting no error got %s", err)
	}

	if err := f.Close(); err != nil {
		t.Fatalf("expecting no error got %s", err)
	}

	t.Run("narinfo does exist in storage", func(t *testing.T) {
		_, err := os.Stat(storePath)
		if err != nil {
			t.Fatalf("expected no error but got: %s", err)
		}
	})

	t.Run("deleteNarInfo does not return an error", func(t *testing.T) {
		p := ts.URL + "/" + narInfoHash1 + ".narinfo"

		r, err := http.NewRequestWithContext(context.Background(), "DELETE", p, nil)
		if err != nil {
			t.Fatalf("error Do(r): %s", err)
		}

		resp, err := ts.Client().Do(r)
		if err != nil {
			t.Fatalf("error Do(r): %s", err)
		}

		if want, got := http.StatusNoContent, resp.StatusCode; want != got {
			t.Errorf("want %d got %d", want, got)
		}
	})

	t.Run("narinfo is gone from the store", func(t *testing.T) {
		_, err := os.Stat(storePath)
		if err == nil {
			t.Fatal("expected an error but got none")
		}
	})
}

//nolint:paralleltest
func TestGetNar(t *testing.T) {
	ts := startServer(t)
	defer ts.Close()

	tu, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("error not expected, got %s", err)
	}

	dir, err := os.MkdirTemp("", "cache-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	uc, err := upstream.New(logger, tu.Host, []string{"cache.nixos.org-1:6NCHdD59X431o0gWypbMrAURkbJ16ZPMQFGspcDShjY="})
	if err != nil {
		t.Fatalf("expected no error, got %s", err)
	}

	c, err := cache.New(logger, "cache.example.com", dir, []upstream.Cache{uc})
	if err != nil {
		t.Errorf("expected no error, got %q", err)
	}

	narName := narHash1 + ".nar"

	t.Run("nar does not exist upstream", func(t *testing.T) {
		_, _, err := c.GetNar("doesnotexist", "")
		if want, got := cache.ErrNotFound, err; !errors.Is(got, want) {
			t.Errorf("want %s got %s", want, got)
		}
	})

	t.Run("nar exists upstream", func(t *testing.T) {
		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(dir, "store", "nar", narName))
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})

		size, r, err := c.GetNar(narHash1, "")
		if err != nil {
			t.Fatalf("no error expected, got: %s", err)
		}
		defer r.Close()

		t.Run("size is correct", func(t *testing.T) {
			if want, got := int64(len(narText1)), size; want != got {
				t.Errorf("want %d got %d", want, got)
			}
		})

		t.Run("body is the same", func(t *testing.T) {
			body, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("expected no error, got: %s", err)
			}

			if want, got := narText1, string(body); want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})

		t.Run("it should now exist in the store", func(t *testing.T) {
			_, err := os.Stat(filepath.Join(dir, "store", "nar", narName))
			if err != nil {
				t.Fatalf("expected no error got %s", err)
			}
		})
	})
}

//nolint:paralleltest
func TestPutNar(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	c, err := cache.New(logger, "cache.example.com", dir, nil)
	if err != nil {
		t.Errorf("expected no error, got %q", err)
	}

	s := server.New(logger, c)

	ts := httptest.NewServer(s)
	defer ts.Close()

	t.Run("without compression", func(t *testing.T) {
		storePath := filepath.Join(dir, "store", "nar", narHash1+".nar")

		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			_, err := os.Stat(storePath)
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})

		t.Run("putNar does not return an error", func(t *testing.T) {
			p := ts.URL + "/nar/" + narHash1 + ".nar"

			r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(narText1))
			if err != nil {
				t.Fatalf("error Do(r): %s", err)
			}

			resp, err := ts.Client().Do(r)
			if err != nil {
				t.Fatalf("error Do(r): %s", err)
			}

			if want, got := http.StatusNoContent, resp.StatusCode; want != got {
				t.Errorf("want %d got %d", want, got)
			}
		})

		t.Run("nar does exist in storage", func(t *testing.T) {
			f, err := os.Open(storePath)
			if err != nil {
				t.Fatalf("expected no error but got: %s", err)
			}

			bs, err := io.ReadAll(f)
			if err != nil {
				t.Fatalf("expected no error but got: %s", err)
			}

			if want, got := narText1, string(bs); want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})
	})

	t.Run("with compression", func(t *testing.T) {
		storePath := filepath.Join(dir, "store", "nar", narHash1+".nar.xz")

		t.Run("nar does not exist in storage yet", func(t *testing.T) {
			_, err := os.Stat(storePath)
			if err == nil {
				t.Fatal("expected an error but got none")
			}
		})

		t.Run("putNar does not return an error", func(t *testing.T) {
			p := ts.URL + "/nar/" + narHash1 + ".nar.xz"

			r, err := http.NewRequestWithContext(context.Background(), "PUT", p, strings.NewReader(narText1))
			if err != nil {
				t.Fatalf("error Do(r): %s", err)
			}

			resp, err := ts.Client().Do(r)
			if err != nil {
				t.Fatalf("error Do(r): %s", err)
			}

			if want, got := http.StatusNoContent, resp.StatusCode; want != got {
				t.Errorf("want %d got %d", want, got)
			}
		})

		t.Run("nar does exist in storage", func(t *testing.T) {
			f, err := os.Open(storePath)
			if err != nil {
				t.Fatalf("expected no error but got: %s", err)
			}

			bs, err := io.ReadAll(f)
			if err != nil {
				t.Fatalf("expected no error but got: %s", err)
			}

			if want, got := narText1, string(bs); want != got {
				t.Errorf("want %q got %q", want, got)
			}
		})
	})
}

//nolint:paralleltest
func TestDeleteNar(t *testing.T) {
	dir, err := os.MkdirTemp("", "cache-path-")
	if err != nil {
		t.Fatalf("expected no error, got: %q", err)
	}
	defer os.RemoveAll(dir) // clean up

	c, err := cache.New(logger, "cache.example.com", dir, nil)
	if err != nil {
		t.Errorf("expected no error, got %q", err)
	}

	s := server.New(logger, c)

	ts := httptest.NewServer(s)
	defer ts.Close()

	storePath := filepath.Join(dir, "store", narHash1+".narinfo")

	t.Run("nar does not exist in storage yet", func(t *testing.T) {
		_, err := os.Stat(storePath)
		if err == nil {
			t.Fatal("expected an error but got none")
		}
	})

	f, err := os.Create(storePath)
	if err != nil {
		t.Fatalf("expecting no error got %s", err)
	}

	if _, err := f.WriteString(narText1); err != nil {
		t.Fatalf("expecting no error got %s", err)
	}

	if err := f.Close(); err != nil {
		t.Fatalf("expecting no error got %s", err)
	}

	t.Run("nar does exist in storage", func(t *testing.T) {
		_, err := os.Stat(storePath)
		if err != nil {
			t.Fatalf("expected no error but got: %s", err)
		}
	})

	t.Run("deleteNar does not return an error", func(t *testing.T) {
		p := ts.URL + "/" + narHash1 + ".narinfo"

		r, err := http.NewRequestWithContext(context.Background(), "DELETE", p, nil)
		if err != nil {
			t.Fatalf("error Do(r): %s", err)
		}

		resp, err := ts.Client().Do(r)
		if err != nil {
			t.Fatalf("error Do(r): %s", err)
		}

		if want, got := http.StatusNoContent, resp.StatusCode; want != got {
			t.Errorf("want %d got %d", want, got)
		}
	})

	t.Run("nar is gone from the store", func(t *testing.T) {
		_, err := os.Stat(storePath)
		if err == nil {
			t.Fatal("expected an error but got none")
		}
	})
}

func startServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/nix-cache-info" {
			if _, err := w.Write([]byte(nixStoreInfo)); err != nil {
				t.Fatalf("expected no error got: %s", err)
			}

			return
		}

		if r.URL.Path == "/"+narInfoHash1+".narinfo" {
			if _, err := w.Write([]byte(narInfoText1)); err != nil {
				t.Fatalf("expected no error got: %s", err)
			}

			return
		}

		if r.URL.Path == "/nar/"+narHash1+".nar" {
			if _, err := w.Write([]byte(narText1)); err != nil {
				t.Fatalf("expected no error got: %s", err)
			}

			return
		}

		if r.URL.Path == "/"+narInfoHash2+".narinfo" {
			if _, err := w.Write([]byte(narInfoText2)); err != nil {
				t.Fatalf("expected no error got: %s", err)
			}

			return
		}

		if r.URL.Path == "/nar/"+narHash2+".nar" {
			if _, err := w.Write([]byte(narText2)); err != nil {
				t.Fatalf("expected no error got: %s", err)
			}

			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
}
