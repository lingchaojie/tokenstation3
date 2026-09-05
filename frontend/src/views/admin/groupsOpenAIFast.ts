export function supportsGroupOpenAIFast(platform: string): boolean {
  return platform === "openai";
}

export function normalizeGroupOpenAIFast(
  platform: string,
  enabled: boolean,
): boolean {
  return supportsGroupOpenAIFast(platform) && enabled;
}
