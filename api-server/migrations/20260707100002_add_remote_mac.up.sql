-- Optional MAC address for Wake-on-LAN of a remote workspace. Empty = no
-- WoL (the machine must be powered on by other means).
ALTER TABLE remote_workspaces ADD COLUMN mac_address TEXT NOT NULL DEFAULT '';
