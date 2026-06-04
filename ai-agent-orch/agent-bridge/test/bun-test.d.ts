declare module 'bun:test' {
    interface Matcher {
        not: Matcher;
        toBe(expected: unknown): void;
        toBeUndefined(): void;
        toContain(expected: string): void;
        toEqual(expected: unknown): void;
        toThrow(expected?: string): void;
    }

    export function describe(name: string, fn: () => void): void;
    export function test(name: string, fn: () => void | Promise<void>): void;
    export function expect(value: unknown): Matcher;
}

interface ImportMeta {
    dir: string;
}
