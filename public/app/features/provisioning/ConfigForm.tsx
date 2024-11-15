import { useEffect, useState } from 'react';
import { Controller, useForm } from 'react-hook-form';
import { useNavigate } from 'react-router-dom-v5-compat';
import { v4 as uuidv4 } from 'uuid';

import { AppEvents } from '@grafana/data';
import { getAppEvents } from '@grafana/runtime';
import { Field, Combobox, SecretInput, Input, Button, Switch } from '@grafana/ui';
import { FormPrompt } from 'app/core/components/FormPrompt/FormPrompt';

import { FolderPicker } from '../../core/components/Select/FolderPicker';

import { useCreateRepositoryMutation } from './api';
import { RepositoryFormData } from './types';
import { dataToSpec } from './utils/data';

const typeOptions = ['GitHub', 'Local', 'S3'].map((label) => ({ label, value: label.toLowerCase() }));

export function ConfigForm() {
  const [submitData, request] = useCreateRepositoryMutation();
  const {
    register,
    handleSubmit,
    reset,
    control,
    formState: { errors, isDirty },
    setValue,
    watch,
    getValues,
  } = useForm<RepositoryFormData>({
    defaultValues: {
      type: 'github',
      token: '',
      owner: '',
      repository: '',
      folder: '',
      branchWorkflow: false,
      generateDashboardPreviews: false,
    },
  });
  const [tokenConfigured, setTokenConfigured] = useState(false);
  const navigate = useNavigate();
  const watchType = watch('type');

  useEffect(() => {
    const appEvents = getAppEvents();
    if (request.isSuccess) {
      const formData = getValues();

      appEvents.publish({
        type: AppEvents.alertSuccess.name,
        payload: ['Repository settings saved'],
      });
      reset(formData);
      setTimeout(() => {
        navigate('/admin/provisioning');
      }, 300);
    }
  }, [request.isSuccess, reset, getValues, navigate]);

  const onSubmit = (data: RepositoryFormData) => {
    const spec = dataToSpec(data);
    submitData({ metadata: { generateName: uuidv4() }, spec });
  };

  return (
    <form onSubmit={handleSubmit(onSubmit)} style={{ maxWidth: 600 }}>
      <FormPrompt onDiscard={reset} confirmRedirect={isDirty} />
      <Field label={'Repository type'}>
        <Controller
          name={'type'}
          control={control}
          render={({ field: { ref, onChange, ...field } }) => {
            return (
              <Combobox
                options={typeOptions}
                onChange={(value) => onChange(value?.value)}
                placeholder={'Select repository type'}
                {...field}
              />
            );
          }}
        />
      </Field>
      {watchType === 'github' && (
        <>
          <Field label={'GitHub token'}>
            <Controller
              name={'token'}
              control={control}
              render={({ field: { ref, ...field } }) => {
                return (
                  <SecretInput
                    {...field}
                    id={'token'}
                    placeholder={'ghp_yourTokenHere1234567890abcdEFGHijklMNOP'}
                    isConfigured={tokenConfigured}
                    onReset={() => {
                      setValue('token', '');
                      setTokenConfigured(false);
                    }}
                  />
                );
              }}
            />
          </Field>
          <Field label={'Repository owner'} error={errors?.owner?.message} invalid={!!errors?.owner}>
            <Input {...register('owner', { required: 'This field is required.' })} placeholder={'test'} />
          </Field>
          <Field label={'Repository name'} error={errors?.repository?.message} invalid={!!errors?.repository}>
            <Input {...register('repository', { required: 'This field is required.' })} placeholder={'example'} />
          </Field>
          <Field label={'Resource folder'}>
            <Controller
              control={control}
              name={'folder'}
              render={({ field: { ref, ...field } }) => {
                return <FolderPicker {...field} />;
              }}
            />
          </Field>
          <Field label={'Commit directly to main'}>
            <Switch {...register('branchWorkflow')} />
          </Field>
          <Field label={'Show dashboard previews'}>
            <Switch {...register('generateDashboardPreviews')} />
          </Field>
        </>
      )}

      {watchType === 'local' && (
        <Field label={'Local path'} error={errors?.path?.message} invalid={!!errors?.path}>
          <Input {...register('path', { required: 'This field is required.' })} placeholder={'/path/to/repo'} />
        </Field>
      )}

      {watchType === 's3' && (
        <>
          <Field label={'S3 bucket'} error={errors?.bucket?.message} invalid={!!errors?.bucket}>
            <Input {...register('bucket', { required: 'This field is required.' })} placeholder={'bucket-name'} />
          </Field>
          <Field label={'S3 region'} error={errors?.region?.message} invalid={!!errors?.region}>
            <Input {...register('region', { required: 'This field is required.' })} placeholder={'us-west-2'} />
          </Field>
        </>
      )}
      <Button type={'submit'}>Save</Button>
    </form>
  );
}
