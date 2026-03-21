package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func authCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate with LLM providers via OAuth",
		Long:  "Manage OAuth authentication via the running gateway. Requires the gateway to be running.",
	}
	cmd.AddCommand(authStatusCmd())
	cmd.AddCommand(authLogoutCmd())
	cmd.AddCommand(authAnthropicCmd())
	return cmd
}

// gatewayURL returns the base URL for the running gateway.
func gatewayURL() string {
	if u := os.Getenv("GOCLAW_GATEWAY_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	host := os.Getenv("GOCLAW_HOST")
	if host == "" {
		host = "127.0.0.1"
	}
	port := os.Getenv("GOCLAW_PORT")
	if port == "" {
		port = "3577"
	}
	return fmt.Sprintf("http://%s:%s", host, port)
}

// gatewayRequest sends an authenticated request to the running gateway.
func gatewayRequest(method, path string) (map[string]any, error) {
	url := gatewayURL() + path
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}

	if token := os.Getenv("GOCLAW_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach gateway at %s: %w", gatewayURL(), err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("invalid response from gateway: %s", string(body))
	}

	if resp.StatusCode >= 400 {
		if msg, ok := result["error"].(string); ok {
			return nil, fmt.Errorf("gateway error: %s", msg)
		}
		return nil, fmt.Errorf("gateway returned status %d", resp.StatusCode)
	}

	return result, nil
}

func authStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show authentication status for all providers",
		Long:  "Check OAuth/token authentication status for OpenAI and Anthropic.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// OpenAI status
			result, err := gatewayRequest("GET", "/v1/auth/openai/status")
			if err == nil {
				if auth, _ := result["authenticated"].(bool); auth {
					name, _ := result["provider_name"].(string)
					fmt.Printf("OpenAI OAuth: active (provider: %s)\n", name)
				} else {
					fmt.Println("OpenAI OAuth: not configured")
				}
			}

			// Anthropic status
			result, err = gatewayRequest("GET", "/v1/auth/anthropic/status")
			if err == nil {
				if auth, _ := result["authenticated"].(bool); auth {
					tokenType, _ := result["token_type"].(string)
					expiresAt, _ := result["expires_at"].(string)
					if tokenType == "setup_token" && expiresAt != "" {
						fmt.Printf("Anthropic: active (setup token, expires %s)\n", expiresAt)
					} else if tokenType == "api_key" {
						fmt.Println("Anthropic: active (API key)")
					} else {
						fmt.Println("Anthropic: active")
					}
				} else {
					fmt.Println("Anthropic: not configured")
				}
			}

			return nil
		},
	}
}

func authLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout [provider]",
		Short: "Remove stored OAuth Tokens",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			provider := "openai"
			if len(args) > 0 {
				provider = args[0]
			}

			switch provider {
			case "openai":
				_, err := gatewayRequest("POST", "/v1/auth/openai/logout")
				if err != nil {
					return err
				}
				fmt.Println("OpenAI OAuth Token removed.")
			case "anthropic":
				_, err := gatewayRequest("POST", "/v1/auth/anthropic/logout")
				if err != nil {
					return err
				}
				fmt.Println("Anthropic credentials removed.")
			default:
				return fmt.Errorf("unknown provider: %s (supported: openai, anthropic)", provider)
			}
			return nil
		},
	}
}
