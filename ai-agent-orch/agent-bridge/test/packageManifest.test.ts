import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'fs';
import { join } from 'path';

interface CommandContribution {
    command: string;
}

interface PackageManifest {
    activationEvents: string[];
    contributes: {
        commands: CommandContribution[];
        menus?: {
            commandPalette?: CommandContribution[];
        };
    };
}

const manifest = JSON.parse(
    readFileSync(join(import.meta.dir, '..', 'package.json'), 'utf8')
) as PackageManifest;

describe('VS Code command manifest', () => {
    test('activates every contributed Bridge command', () => {
        for (const contribution of manifest.contributes.commands) {
            expect(manifest.activationEvents).toContain(`onCommand:${contribution.command}`);
        }
    });

    test('shows every Bridge command in the command palette', () => {
        const commandPalette = manifest.contributes.menus?.commandPalette || [];
        const commandPaletteIDs = commandPalette.map((item) => item.command);

        for (const contribution of manifest.contributes.commands) {
            expect(commandPaletteIDs).toContain(contribution.command);
        }
    });
});
