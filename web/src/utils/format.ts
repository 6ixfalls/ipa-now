export function formatLabel(value: string): string {
  return value.replaceAll("-", " ").replaceAll("_", " ");
}
export function formatBytes(value: number): string {
  if (value >= 1073741824) return `${(value / 1073741824).toFixed(1)} GB`;
  if (value >= 1048576) return `${(value / 1048576).toFixed(1)} MB`;
  if (value >= 1024) return `${Math.round(value / 1024)} KB`;
  return `${value} B`;
}
