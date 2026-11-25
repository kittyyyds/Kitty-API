package service

// GeminiTokenCache is the platform-neutral TokenCache used by the Gemini OAuth /
// Vertex token providers. Upstream (Wei-Shaw) names it GeminiTokenCache; this fork
// unified all providers on TokenCache, so keep the Gemini name as an alias.
type GeminiTokenCache = TokenCache
