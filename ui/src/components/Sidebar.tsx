/**
 * SUPERSEDED — mail now uses the shared Sidebar from `@cloistr/ui/components`.
 *
 * This file used to hold mail's own implementation. It had the mobile drawer
 * but no desktop icons-only collapse, and every other app had rolled its own
 * variant too: different hamburger glyphs, different open/close behaviour, and
 * four apps that independently picked a z-index BELOW the sticky header, so
 * each painted the header over the top of its own open drawer.
 *
 * It is a re-export rather than a deletion so that no second implementation can
 * quietly reappear here: anything that still imports `./Sidebar` gets the
 * shared component, not a copy that can drift.
 *
 * Note the prop contract differs deliberately — the shared component is
 * CONTROLLED (`open`/`onOpenChange`, `collapsed`/`onCollapsedChange`) because
 * the desktop collapse is a preference the app persists. The old local props
 * were `isOpen`/`onClose`. A stale call site therefore fails loudly at the type
 * level instead of silently rendering something half-wired.
 *
 * Import from `@cloistr/ui/components` directly in new code.
 */
export { Sidebar as default, Sidebar, SidebarToggle } from '@cloistr/ui/components'
export type { SidebarProps, SidebarItem } from '@cloistr/ui/components'
