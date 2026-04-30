import { useParams, Link } from 'react-router-dom';

export default function CatalogDetailPage() {
  const { id } = useParams();
  return (
    <div className="p-6">
      <Link to="/catalog" className="text-sm text-zinc-400 hover:text-zinc-200">← Back to Catalog</Link>
      <h1 className="text-xl font-semibold text-zinc-100 mt-2">IOC #{id}</h1>
      <p className="text-sm text-zinc-500 mt-1">Detail page — coming in Task G.</p>
    </div>
  );
}
