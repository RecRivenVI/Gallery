import { useState } from 'react';

export function WorkCover({
  title,
  coverMediaId,
  queryPublicationId,
  canReadMedia,
  alt = '',
  className
}: {
  title: string;
  coverMediaId?: string | null;
  queryPublicationId: string;
  canReadMedia: boolean;
  alt?: string;
  className?: string;
}) {
  const source =
    canReadMedia && coverMediaId
      ? `/api/v1/media/${encodeURIComponent(coverMediaId)}/content?queryPublicationId=${encodeURIComponent(queryPublicationId)}`
      : undefined;
  const [failedSource, setFailedSource] = useState<string>();
  const showImage = source !== undefined && failedSource !== source;

  return (
    <div className={['work-cover', className].filter(Boolean).join(' ')} aria-hidden={alt ? undefined : true}>
      {showImage ? (
        <img src={source} alt={alt} loading="lazy" decoding="async" onError={() => setFailedSource(source)} />
      ) : (
        <span role={alt ? 'img' : undefined} aria-label={alt || undefined}>
          {title.trim().slice(0, 1).toUpperCase() || '—'}
        </span>
      )}
    </div>
  );
}
