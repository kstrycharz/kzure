import { randomBytes } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
const root = resolve(import.meta.dirname, '..');
mkdirSync(resolve(root, '.local'), { recursive: true });
if (!existsSync(resolve(root, '.env'))) {
  const password = randomBytes(24).toString('base64url');
  const values = {
    KZURE_INSTALL_ID: randomBytes(16).toString('hex'),
    KZURE_ADMIN_EMAIL: 'admin@kzure.local',
    KZURE_ADMIN_PASSWORD: password,
    KZURE_DB_PASSWORD: randomBytes(32).toString('hex'),
    KZURE_AGENT_TOKEN: randomBytes(32).toString('hex'),
    KZURE_ENCRYPTION_KEY: randomBytes(32).toString('base64'),
    KZURE_PORT: '8088',
  };
  writeFileSync(resolve(root, '.env'), Object.entries(values).map(([k,v]) => `${k}=${v}`).join('\n') + '\n', {mode:0o600});
  writeFileSync(resolve(root, '.local/admin-credentials.txt'), `Kzure local development administrator\nURL: http://localhost:8088\nEmail: ${values.KZURE_ADMIN_EMAIL}\nPassword: ${password}\n\nKeep this file private. Existing database passwords are not reset by changing .env.\n`, {mode:0o600});
  console.log('Generated installation configuration and private administrator credentials in .local/admin-credentials.txt');
} else {
  console.log('Reusing existing installation configuration.');
}
if (!existsSync(resolve(root,'.local/images.lock.json'))) writeFileSync(resolve(root,'.local/images.lock.json'),'{}\n');
if (!readFileSync(resolve(root,'.env'),'utf8').includes('KZURE_INSTALL_ID=')) throw new Error('Existing .env is missing Kzure configuration');
