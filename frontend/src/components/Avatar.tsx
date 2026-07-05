import type { User } from '@/types';

// Deterministic identicon-style avatar: initials on a hue derived from the
// username, so the same user always gets the same color. No image storage.
const PALETTE = [
  'bg-rose-500',
  'bg-orange-500',
  'bg-amber-500',
  'bg-emerald-500',
  'bg-teal-500',
  'bg-sky-500',
  'bg-indigo-500',
  'bg-fuchsia-500',
];

const SIZES = {
  sm: 'h-8 w-8 text-xs',
  md: 'h-10 w-10 text-sm',
  lg: 'h-16 w-16 text-xl',
} as const;

function initialsOf(user: Pick<User, 'username' | 'displayName'>): string {
  const source = (user.displayName || user.username).trim();
  const parts = source.split(/\s+/);
  const initials =
    parts.length >= 2 ? parts[0][0] + parts[parts.length - 1][0] : source.slice(0, 2);
  return initials.toUpperCase();
}

function colorOf(username: string): string {
  let hash = 0;
  for (const ch of username) {
    hash = (hash * 31 + ch.charCodeAt(0)) >>> 0;
  }
  return PALETTE[hash % PALETTE.length];
}

export function Avatar({
  user,
  size = 'md',
}: {
  user: Pick<User, 'username' | 'displayName'>;
  size?: keyof typeof SIZES;
}) {
  return (
    <span
      className={`inline-flex select-none items-center justify-center rounded-full font-semibold text-white ${colorOf(user.username)} ${SIZES[size]}`}
    >
      {initialsOf(user)}
    </span>
  );
}
