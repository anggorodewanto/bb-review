import {StrictMode} from 'react';
import {createRoot} from 'react-dom/client';
import {BrowserRouter} from 'react-router-dom';
import {QueryClient, QueryClientProvider} from '@tanstack/react-query';
import 'react-diff-view/style/index.css';
import './styles.css';
import {App} from './App';

const queryClient = new QueryClient({
    defaultOptions: {
        queries: {
            staleTime: 15_000,
            retry: 1,
            refetchOnWindowFocus: true,
        },
    },
});

const container = document.getElementById('root');
if (!container) {
    throw new Error('Missing #root element');
}

createRoot(container).render(
    <StrictMode>
        <QueryClientProvider client={queryClient}>
            <BrowserRouter future={{v7_startTransition: true, v7_relativeSplatPath: true}}>
                <App />
            </BrowserRouter>
        </QueryClientProvider>
    </StrictMode>,
);
