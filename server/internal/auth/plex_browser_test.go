package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestPlexBrowserPKCEOnHTTPAndHTTPS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is needed to execute the browser PKCE helper")
	}
	// The RFC 7636 vector plus random verifiers cross-check both browser paths
	// against Go's SHA-256. No provider account or network is involved.
	vectors := []string{"dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"}
	for i := 0; i < 128; i++ {
		v, err := randomURLToken(32)
		if err != nil {
			t.Fatal(err)
		}
		vectors = append(vectors, v)
	}
	input, _ := json.Marshal(vectors)
	script := `const fs=require('node:fs');const wc=require('node:crypto').webcrypto;
 const bufferToB64url=buffer=>Buffer.from(buffer).toString('base64url');
 ` + plexPKCEScript + `
 (async()=>{const vectors=JSON.parse(fs.readFileSync(0,'utf8'));const out=[];
 for(const subtle of [undefined,wc.subtle]) {
 Object.defineProperty(globalThis,'crypto',{value:{subtle},configurable:true});
 for(const v of vectors)out.push(await plexChallenge(v));
 for(const v of ['', 'a'.repeat(44), '!'.repeat(43)]) {let refused=false;try{await plexChallenge(v)}catch(_){refused=true}if(!refused)throw Error('invalid verifier accepted')}
 }process.stdout.write(JSON.stringify(out));})().catch(e=>{console.error(e);process.exit(1)});`
	cmd := exec.Command(node, "-e", script)
	cmd.Stdin = strings.NewReader(string(input))
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("browser helper: %v %s", err, raw)
	}
	var output []string
	if err = json.Unmarshal(raw, &output); err != nil || len(output) != 2*len(vectors) {
		t.Fatal("invalid browser result", err)
	}
	for i, actual := range output {
		hash := sha256.Sum256([]byte(vectors[i%len(vectors)]))
		if actual != base64.RawURLEncoding.EncodeToString(hash[:]) {
			t.Fatalf("browser challenge differs at vector %d", i)
		}
	}
}
