import { mount } from 'svelte';
import './app.css';
import './themes.css';
import App from './App.svelte';

mount(App, { target: document.getElementById('app') });
