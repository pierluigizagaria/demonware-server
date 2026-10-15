package steam

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// This child models only the private pipe contract. It never loads Valve code.
func TestMain(m *testing.M) {
	if scenario := os.Getenv("OSW_HELPER_TEST_CHILD"); scenario != "" {
		if scenario == "startup-failure" {
			os.Exit(15)
		}
		fmt.Println("OSW1\tREADY\t99")
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			parts := strings.Split(scanner.Text(), "\t")
			if len(parts) != 4 {
				os.Exit(2)
			}
			if scenario == "malformed" {
				fmt.Println("bad protocol")
				continue
			}
			if scenario == "timeout" {
				continue
			}
			if parts[2] == "END" {
				fmt.Printf("OSW1\tRESULT\t%s\tENDED\n", parts[1])
				continue
			}
			fmt.Printf("OSW1\tRESULT\t%s\tOK\t76561198000000001\t76561198000000001\t1\n", parts[1])
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestHelperPipeLifecycleAndFailure(t *testing.T) {
	for _, scenario := range []string{"success", "startup-failure", "malformed", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("OSW_HELPER_TEST_CHILD", scenario)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			h, err := StartHelper(ctx, os.Args[0], "")
			if scenario == "startup-failure" {
				if err == nil {
					h.Close()
					t.Fatal("helper startup failure was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			if h.ServerID() != 99 {
				t.Fatal("wrong helper identity")
			}
			request, cancelRequest := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancelRequest()
			validated, err := h.Validate(request, fixtureToken)
			if scenario == "success" {
				if err != nil || validated.AccountID != 76561198000000001 || validated.Lease != 1 {
					t.Fatal("valid helper result missing")
				}
				h.End(validated.Lease)
			} else if err == nil {
				t.Fatal("failed helper admitted identity")
			}
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := h.Validate(ctx, fixtureToken); err == nil {
				t.Fatal("closed helper admitted")
			}
		})
	}
}
