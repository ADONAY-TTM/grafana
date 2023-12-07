import { css } from '@emotion/css';
import React, { Suspense, useMemo, useState } from 'react';

import {
  GrafanaTheme2,
  PluginExtensionComponent,
  PluginExtensionGlobalDrawerContext,
  PluginExtensionPoints,
} from '@grafana/data';
import { getPluginComponentExtensions } from '@grafana/runtime';
import { Drawer, IconButton, useStyles2 } from '@grafana/ui';
import { useSelector } from 'app/types';

type DrawerSize = 'sm' | 'md' | 'lg';

export interface Props {
  open: boolean;
  onClose: () => void;
  selectedTab?: string;
  onChangeTab: (id?: string) => void;
}

export function ExtensionDrawer({ open, onClose, selectedTab }: Props) {
  const [title, setTitle] = useState<React.ReactNode | undefined>();
  const [subtitle, setSubTitle] = useState<React.ReactNode | undefined>();
  const dragData = useSelector((state) => state.dragDrop.data);
  const styles = useStyles2(getStyles);
  const [size, setSize] = useState<DrawerSize>('md');
  const extensions: Array<PluginExtensionComponent<PluginExtensionGlobalDrawerContext>> = useMemo(() => {
    const extensionPointId = PluginExtensionPoints.GlobalDrawer;
    const { extensions } = getPluginComponentExtensions({ extensionPointId, context: { setSubTitle, setTitle } });
    return extensions;
  }, []);

  const activeTab = selectedTab ?? extensions[0]?.id;

  const children = useMemo(
    () =>
      extensions.map(
        (extension, index) =>
          activeTab === extension.id && (
            // Support lazy components with a fallback.
            <Suspense key={index} fallback={'Loading...'}>
              <extension.component context={{ dragData, setSubTitle, setTitle }} />
            </Suspense>
          )
      ),
    [activeTab, extensions, dragData]
  );

  const [buttonIcon, buttonLabel, newSize] =
    size === 'lg'
      ? (['gf-movepane-left', 'Narrow drawer', 'md'] as const)
      : (['gf-movepane-right', 'Widen drawer', 'lg'] as const);

  return (
    open && (
      <Drawer
        onClose={onClose}
        title={title}
        subtitle={
          <div className={styles.wrapper}>
            {subtitle}
            <IconButton
              name={buttonIcon}
              aria-label={buttonLabel}
              tooltip={buttonLabel}
              onClick={() => setSize(newSize)}
            />
          </div>
        }
        size={size}
        closeOnMaskClick
      >
        {children}
      </Drawer>
    )
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  wrapper: css({
    display: 'flex',
    gap: theme.spacing(0.5),
    justifyContent: 'space-between',
    alignItems: 'flex-start',
  }),
});
