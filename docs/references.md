# Verified implementation references

- [my-itmo-api 2.3.1](https://github.com/alllexey-dev/my-itmo-api/tree/4243d3a968e7ab517cd615d7a960cec35afc2b9f):
  ITMO.ID PKCE/password form, client `student-personal-cabinet`, callback
  `https://my.itmo.ru/login/callback`, token forms, schedule endpoint and models.
  The refresh form preserves the observed plural `scopes=openid profile` field.
  This service is an independent Go implementation of that observed wire flow.
- [Official Go MCP SDK v1.8.0](https://github.com/modelcontextprotocol/go-sdk/tree/v1.8.0):
  typed tool registration and stateless Streamable HTTP with JSON responses.
  Exact dependency versions/checksums are in `go.mod`/`go.sum`.
- [ITMO.ID discovery](https://id.itmo.ru/auth/realms/itmo/.well-known/openid-configuration):
  issuer, authorization and token endpoint metadata. The realm advertising a
  grant does not prove a particular client
  permits it; this service uses the observed authorization-code flow with PKCE.
- [Codex MCP configuration](https://developers.openai.com/codex/mcp/):
  Streamable HTTP, static HTTP headers and `env_http_headers`.
- [Caddy request logging](https://caddyserver.com/docs/caddyfile/directives/log)
  and [runtime logging](https://caddyserver.com/docs/caddyfile/options#log):
  custom credential headers require explicit runtime filters. Caddy's built-in
  sensitive-header list does not cover the two custom ITMO headers.

The upstream ITMO API is undocumented. Public source and unauthenticated probes
establish the expected protocol; a credentialed live test is still required to
claim successful login, refresh and personal schedule retrieval.

## Reference license

The reference implementation is MIT-licensed. Its notice is retained here for
the authentication/field mapping work informed by that implementation:

MIT License

Copyright (c) 2025 alllexey

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
