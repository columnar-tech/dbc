import { spawn } from 'child_process';

const child = spawn('npx', ['shadcn-svelte@latest', 'init', '--no-deps'], {
  stdio: ['pipe', 'pipe', 'pipe']
});

let output = '';

child.stdout.on('data', (data) => {
  const str = data.toString();
  output += str;
  process.stdout.write(str);
  
  if (str.includes('how would you like to continue?')) {
    // Choose from a list of pre-configured presets
    child.stdin.write('\n');
  } else if (str.includes('Choose from a list of pre-configured presets')) {
    // vega
    child.stdin.write('\n');
  } else if (str.includes('Where is your global CSS file?')) {
    child.stdin.write('\n');
  } else if (str.includes('Where is your tailwind.config.js located?')) {
    child.stdin.write('\n');
  } else if (str.includes('Configure the import alias for components:')) {
    child.stdin.write('\n');
  } else if (str.includes('Configure the import alias for utils:')) {
    child.stdin.write('\n');
  } else if (str.includes('Configure the import alias for ui:')) {
    child.stdin.write('\n');
  } else if (str.includes('Configure the import alias for hooks:')) {
    child.stdin.write('\n');
  } else if (str.includes('Are you sure you want to proceed?')) {
    child.stdin.write('y\n');
  } else if (str.includes('Where is your tsconfig/jsconfig file?')) {
    child.stdin.write('\n');
  } else if (str.includes('Would you like to overwrite your existing components?')) {
    child.stdin.write('y\n');
  }
});

child.stderr.on('data', (data) => {
  process.stderr.write(data.toString());
});

child.on('close', (code) => {
  console.log(`child process exited with code ${code}`);
});
