-- Quick account switch (docs/superpowers/specs/quick-account-switch-design.md): devices that share
-- a link_group were signed in on the same browser or app and may switch to each other without a password.
ALTER TABLE devices ADD COLUMN link_group TEXT;
CREATE INDEX devices_link_group ON devices(link_group) WHERE link_group IS NOT NULL;
