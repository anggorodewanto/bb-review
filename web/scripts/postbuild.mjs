// vite's `emptyOutDir` wipes web/dist, including the tracked .gitkeep.
// Recreate it so the tracked placeholder stays in the working tree.
import {writeFile} from 'node:fs/promises';

await writeFile(new URL('../dist/.gitkeep', import.meta.url), '');
