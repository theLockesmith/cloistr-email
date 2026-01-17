import { useAuth } from '../lib/api'

export default function SettingsPage() {
  const { keyAPI } = useAuth()

  return (
    <div className="p-6">
      <h1 className="text-3xl font-bold mb-6">Settings</h1>

      <div className="bg-white rounded shadow p-6 max-w-2xl">
        <h2 className="text-xl font-semibold mb-4">Account Settings</h2>

        <div className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Encryption Method
            </label>
            <select className="px-3 py-2 border border-gray-300 rounded">
              <option>NIP-44 (Recommended)</option>
              <option>None</option>
            </select>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Default Encryption
            </label>
            <label className="flex items-center">
              <input type="checkbox" className="w-4 h-4" />
              <span className="ml-2 text-sm text-gray-700">
                Always encrypt emails by default
              </span>
            </label>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-700 mb-1">
              Prompt for Non-Nostr Recipients
            </label>
            <label className="flex items-center">
              <input type="checkbox" checked className="w-4 h-4" />
              <span className="ml-2 text-sm text-gray-700">
                Ask before sending to recipients without Nostr keys
              </span>
            </label>
          </div>
        </div>

        <div className="mt-6 pt-6 border-t">
          <h3 className="text-lg font-semibold mb-4">Nostr Identity</h3>
          <p className="text-sm text-gray-600 mb-4">
            Your email is encrypted and verified using your Nostr keypair.
          </p>
          <button className="px-4 py-2 text-gray-700 border border-gray-300 rounded hover:bg-gray-50">
            View My Public Key
          </button>
        </div>

        <div className="mt-6 flex space-x-3">
          <button className="px-4 py-2 bg-blue-600 text-white rounded hover:bg-blue-700">
            Save Settings
          </button>
          <button className="px-4 py-2 text-gray-700 border border-gray-300 rounded hover:bg-gray-50">
            Cancel
          </button>
        </div>
      </div>
    </div>
  )
}
