/** The volume factor for a track: attenuation only, and only when the user's switch is on. */
export function gainFactor(gainDb: number | null | undefined, on: boolean): number {
  if (!on || gainDb == null || !Number.isFinite(gainDb) || gainDb >= 0) return 1;
  return Math.min(1, Math.max(0, 10 ** (gainDb / 20)));
}
