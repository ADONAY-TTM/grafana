import React, { useEffect, useState } from 'react';
import Skeleton from 'react-loading-skeleton';

import { DataSourceApi, DataSourceOptionsType, DataSourceQueryType, QueryEditorProps } from '@grafana/data';
import { DataQuery, DataSourceJsonData } from '@grafana/schema';

import { instanceOfMigrationHandler, migrateQuery } from '../utils/migrationHandler';

// QueryEditorWithMigration is a higher order component that wraps the QueryEditor component
// and ensures that the query is migrated before being passed to the QueryEditor.
export function QueryEditorWithMigration<
  DSType extends DataSourceApi<TQuery, TOptions>,
  TQuery extends DataQuery = DataSourceQueryType<DSType>,
  TOptions extends DataSourceJsonData = DataSourceOptionsType<DSType>,
>(QueryEditor: React.ComponentType<QueryEditorProps<DSType, TQuery, TOptions>>) {
  const WithExtra = (props: QueryEditorProps<DSType, TQuery, TOptions>) => {
    const [migrated, setMigrated] = useState(false);
    const [query, setQuery] = useState(props.query);

    useEffect(() => {
      if (props.query && instanceOfMigrationHandler(props.datasource)) {
        migrateQuery(props.datasource, props.query).then((migrated) => {
          props.onChange(migrated);
          setQuery(migrated);
          setMigrated(true);
        });
      }
      setMigrated(true);
    }, []); // eslint-disable-line react-hooks/exhaustive-deps

    useEffect(() => {
      setQuery(props.query);
    }, [props.query]);

    if (!migrated) {
      return <Skeleton />;
    }
    return <QueryEditor {...props} query={query} />;
  };
  return WithExtra;
}