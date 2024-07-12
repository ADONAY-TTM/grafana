import { css } from '@emotion/css';
import { memo, useEffect } from 'react';
import AutoSizer from 'react-virtualized-auto-sizer';

import { GrafanaTheme2 } from '@grafana/data';
import { FilterInput, Stack, EmptyState, useStyles2 } from '@grafana/ui';
import { Page } from 'app/core/components/Page/Page';
import { t } from 'app/core/internationalization';
import { ActionRow } from 'app/features/search/page/components/ActionRow';
import { getGrafanaSearcher } from 'app/features/search/service';
import { SearchState } from 'app/features/search/types';

import { useDispatch } from '../../types';

import { useRecentlyDeletedStateManager } from './api/useRecentlyDeletedStateManager';
import { RecentlyDeletedActions } from './components/RecentlyDeletedActions';
import { SearchView } from './components/SearchView';
import { getFolderPermissions } from './permissions';
import { setAllSelection } from './state';

const RecentlyDeletedPage = memo(() => {
  const dispatch = useDispatch();
  const styles = useStyles2(getStyles);

  const [searchState, stateManager] = useRecentlyDeletedStateManager();

  const { canEditFolders, canEditDashboards } = getFolderPermissions();
  const canSelect = canEditFolders || canEditDashboards;

  useEffect(() => {
    stateManager.initStateFromUrl(undefined);

    // Clear selected state when folderUID changes
    dispatch(
      setAllSelection({
        isSelected: false,
        folderUID: undefined,
      })
    );
  }, [dispatch, stateManager]);

  return (
    <Page navId="dashboards/recently-deleted">
      <Page.Contents className={styles.pageContents}>
        <FilterInput
          placeholder={t('recentlyDeleted.filter.placeholder', 'Search for dashboards')}
          value={searchState.query}
          escapeRegex={false}
          onChange={stateManager.onQueryChange}
        />
        <ActionRow
          state={searchState}
          getTagOptions={stateManager.getTagOptions}
          getSortOptions={getGrafanaSearcher().getSortOptions}
          sortPlaceholder={getGrafanaSearcher().sortPlaceholder}
          onLayoutChange={stateManager.onLayoutChange}
          onSortChange={stateManager.onSortChange}
          onTagFilterChange={stateManager.onTagFilterChange}
          onDatasourceChange={stateManager.onDatasourceChange}
          onPanelTypeChange={stateManager.onPanelTypeChange}
          onSetIncludePanels={stateManager.onSetIncludePanels}
        />
        <RecentlyDeletedActions />
        <div className={styles.subView}>
          <AutoSizer>
            {({ width, height }) => (
              <SearchView
                canSelect={canSelect}
                width={width}
                height={height}
                searchStateManager={stateManager}
                searchState={searchState}
                emptyState={<RecentlyDeletedEmptyState searchState={searchState} />}
              />
            )}
          </AutoSizer>
        </div>
      </Page.Contents>
    </Page>
  );
});

interface RecentlyDeletedEmptyStateProps {
  searchState: SearchState;
}

const RecentlyDeletedEmptyState = ({ searchState }: RecentlyDeletedEmptyStateProps) => {
  const userIsSearching = Boolean(searchState.query || searchState.tag.length);
  return (
    <EmptyState
      message={
        userIsSearching
          ? t('recently-deleted.page.no-search-result', 'No results found for your query')
          : t('recently-deleted.page.no-deleted-dashboards', "You haven't deleted any dashboards recently.")
      }
      variant={userIsSearching ? 'not-found' : 'completed'}
      role="alert"
    />
  );
};

const getStyles = (theme: GrafanaTheme2) => ({
  pageContents: css({
    display: 'grid',
    gridTemplateRows: 'auto auto auto 1fr',
    height: '100%',
    rowGap: theme.spacing(1),
  }),

  // AutoSizer needs an element to measure the full height available
  subView: css({
    height: '100%',
  }),
});

RecentlyDeletedPage.displayName = 'RecentlyDeletedPage';
export default RecentlyDeletedPage;
