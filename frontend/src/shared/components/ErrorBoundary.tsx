import { Component, ReactNode } from 'react';

interface Props {
  children: ReactNode;
  fallback?: ReactNode;
  name?: string;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { hasError: false, error: null };

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error) {
    console.error('[ErrorBoundary]', this.props.name ?? '', error);
  }

  render() {
    if (this.state.hasError) {
      return (
        this.props.fallback ?? (
          <div className="flex items-center justify-center p-4 rounded-lg bg-red-950/30 border border-red-800/50 text-red-400 text-sm">
            {this.props.name ? `${this.props.name}: ` : ''}Widget failed to render
          </div>
        )
      );
    }
    return this.props.children;
  }
}
