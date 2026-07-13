/** Theme-matched brand logo (public/logos/): the black-text variant on
 * light surfaces, the white-text one in dark mode. Purely decorative —
 * wrap it in a button/link for behavior. */
export function BrandLogo({ className = 'h-8' }: { className?: string }) {
  return (
    <>
      <img src="/logos/logo_white.png" alt="" className={`${className} dark:hidden`} />
      <img src="/logos/logo_dark.png" alt="" className={`hidden ${className} dark:block`} />
    </>
  );
}
