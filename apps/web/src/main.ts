import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { createWorkbenchRouter } from './app/router'
import './styles/tokens.css'
import './styles/base.css'
import App from './App.vue'

createApp(App).use(createPinia()).use(createWorkbenchRouter()).mount('#app')
