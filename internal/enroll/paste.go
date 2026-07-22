package enroll

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// PasteIDToken returns an IDTokenFn that guides the user to the web enroll page,
// then reads a single pasted Google id_token line from r. Used instead of the
// loopback OAuth flow so enroll works on headless/SSH boxes (browser on the
// user's laptop, agent on a remote Linux host) with no port or tunnel.
func PasteIDToken(enrollURL string, r io.Reader) IDTokenFn {
	return func() (string, error) {
		fmt.Printf(
			"\n브라우저에서 아래 주소를 열어 회사 Google 계정으로 로그인한 뒤,\n"+
				"화면에 표시된 토큰을 복사해 여기에 붙여넣고 Enter를 누르세요:\n\n  %s\n\n토큰> ",
			enrollURL)
		line, err := bufio.NewReader(r).ReadString('\n')
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("read pasted token: %w", err)
		}
		token := strings.TrimSpace(line)
		if token == "" {
			return "", fmt.Errorf("빈 토큰입니다 — 웹 페이지에 표시된 토큰을 붙여넣으세요")
		}
		return token, nil
	}
}
