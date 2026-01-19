import { useAuth } from '../lib/useAuth'
import { truncatePubkey, hasNostrExtension, hasNip44Support } from '../lib/nostr'

export default function Header() {
  const { user, pubkey, logout } = useAuth()
  const hasExtension = hasNostrExtension()
  const hasNip44 = hasNip44Support()

  return (
    <header className="bg-white shadow">
      <div className="px-6 py-4 flex justify-between items-center">
        <div className="flex items-center gap-4">
          <h2 className="text-xl font-semibold text-gray-900">Email</h2>
          {/* Extension status indicator */}
          {hasExtension && (
            <span
              className={`text-xs px-2 py-1 rounded ${
                hasNip44
                  ? 'bg-green-100 text-green-700'
                  : 'bg-yellow-100 text-yellow-700'
              }`}
              title={hasNip44 ? 'NIP-44 encryption available' : 'Extension detected (no NIP-44)'}
            >
              {hasNip44 ? 'NIP-44 Ready' : 'Extension'}
            </span>
          )}
        </div>

        <div className="flex items-center gap-4">
          {(user || pubkey) && (
            <>
              <div className="text-right">
                {user?.email ? (
                  <div className="text-sm font-medium text-gray-900">
                    {user.email}
                  </div>
                ) : pubkey ? (
                  <div className="text-sm font-medium text-gray-900">
                    {truncatePubkey(pubkey, 6)}
                  </div>
                ) : null}
                {pubkey && user?.email && (
                  <div className="text-xs text-gray-500">
                    {truncatePubkey(pubkey, 6)}
                  </div>
                )}
              </div>
              <button
                onClick={logout}
                className="px-3 py-1.5 text-sm font-medium text-gray-700 border border-gray-300 rounded-lg hover:bg-gray-50 transition"
              >
                Logout
              </button>
            </>
          )}
        </div>
      </div>
    </header>
  )
}
