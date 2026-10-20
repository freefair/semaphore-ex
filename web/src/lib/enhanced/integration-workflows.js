import axios from 'axios';

export default async function loadIntegrationWorkflows(projectId) {
  try {
    const response = await axios.get(`/api/project/${projectId}/workflows`);
    return response.data || [];
  } catch (error) {
    // Integration managers may have no workflow visibility; template targets still work.
    if (error.response?.status === 403) return [];
    throw error;
  }
}
