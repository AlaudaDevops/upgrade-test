# GitLab Upgrade Testing Framework

This directory contains a configuration-driven testing framework for GitLab upgrade scenarios using Kubernetes and GitLab Operator.

## Overview

The testing framework allows you to:

- Dynamically create GitLab instances using Kubernetes operator
- Create test data in GitLab according to configuration
- Verify that data exists correctly after operations
- Clean up test data when needed
- Customize test scenarios through simplified YAML configuration files

## Prerequisites

- Kubernetes cluster with GitLab Operator installed
- Python 3.7+ with required dependencies
- Access to GitLab instance
- Project export file for testing

## Installation

1. Install Python dependencies:

```bash
pip install -r requirements.txt
```

2. Configure Kubernetes access:

```bash
# Ensure kubectl is configured to access your cluster
kubectl cluster-info
```

## Configuration

### Test Configuration File

The main configuration file is `config.yaml`. This file defines:

1. **GitLab Instance**: Configuration for creating/managing GitLab instances
2. **Import Project**: A single project to be imported for testing repository data preparation
3. **Version Group**: The top-level group that contains all test data
4. **Generation Rules**: Rules for automatically generating sub-groups and projects

### Configuration Structure

```yaml
# GitLab instance configuration
gitlab:
  namespace: "testing-gitlab-upgrade"
  name: "upgrade-test-gitlab"
  values_file: "testdata/gitlab-template.yaml"
  gitlab_root_password: "07Apples@"
  timeout: 600

# Import project configuration
import_project:
  file_path: "testdata/repo/test-upgrade-repo_export.tar.gz"
  project_name: "test-upgrade-repo"

# Version group configuration
version_group:
  name: "v17.4.2"
  path: "v17.4.2"

# Generation rules for sub-groups and projects
generation_rules:
  sub_groups_count: 3
  projects_per_sub_group: 3
  sub_group_prefix: "group"
  project_prefix: "project"
```

### Configuration Parameters

#### GitLab Instance
- `namespace`: Kubernetes namespace for GitLab instance
- `name`: Name of the GitLab instance
- `values_file`: Path to GitLab template configuration
- `gitlab_root_password`: Root password for GitLab instance
- `timeout`: Timeout for GitLab instance operations (seconds)

#### Import Project
- `file_path`: Path to the project export file
- `project_name`: Name for the imported project

#### Version Group
- `name`: Name of the version group
- `path`: Path of the version group

#### Generation Rules
- `sub_groups_count`: Number of sub-groups to create under the version group
- `projects_per_sub_group`: Number of projects to create in each sub-group
- `sub_group_prefix`: Prefix for sub-group names (e.g., "group" creates group1, group2, etc.)
- `project_prefix`: Prefix for project names (e.g., "project" creates project1, project2, etc.)

## Usage

### Running Tests

1. **Setup**: Ensure you have access to a Kubernetes cluster with GitLab Operator
2. **Configuration**: Modify `config.yaml` to match your test requirements
3. **Execution**: Run the test file:

```bash
# Run specific test categories
STAGE=prepare make run
```

### Test Methods

The test class provides the following methods:

- `test_create_gitlab_instance()`: Creates GitLab instance using Kubernetes operator
- `test_prepare_gitlab_data()`: Creates test data according to configuration
- `test_upgrade_gitlab_instance()`: Triggers GitLab upgrade
- `test_check_gitlab_group_data()`: Verifies that all groups exist correctly
- `test_check_gitlab_project_data()`: Verifies that all projects exist correctly
- `test_cleanup_test_data()`: Cleans up test data (optional)

### Customizing Test Data

To customize the test data:

1. **Edit Configuration**: Modify `config.yaml`
2. **Adjust Generation Rules**: Change `sub_groups_count` and `projects_per_sub_group` to control data volume
3. **Update Import File**: Place project export file in `testdata/repo/` and update `file_path`
4. **Customize Prefixes**: Change `sub_group_prefix` and `project_prefix` for different naming schemes

### Example Configurations

#### Minimal Test (1 group, 1 project)
```yaml
generation_rules:
  sub_groups_count: 1
  projects_per_sub_group: 1
  sub_group_prefix: "test"
  project_prefix: "app"
```

#### Large Scale Test (10 groups, 5 projects each)
```yaml
generation_rules:
  sub_groups_count: 10
  projects_per_sub_group: 5
  sub_group_prefix: "team"
  project_prefix: "service"
```

## File Structure

```
testing/
├── upgrade_gitlab_test.py      # Main test file
├── gitlab_operator.py          # GitLab operations wrapper
├── gitlab_template.py          # GitLab instance template generator
├── config.yaml                 # Test configuration
├── pytest.ini                 # Pytest configuration
├── requirements.txt            # Python dependencies
├── testdata/
│   ├── gitlab-template.yaml    # GitLab instance template
│   └── repo/                   # Project export files
│       └── test-upgrade-repo_export.tar.gz
└── README.md                   # This file
```

## Key Components

### GitLabTemplateGenerator
- Dynamically generates GitLab instance configurations
- Automatically discovers available Kubernetes nodes
- Finds available NodePort ports
- Creates GitLab instances using Kubernetes operator

### GitlabOperator
- Manages GitLab operations via API
- Handles authentication and token management
- Creates users, groups, and projects
- Imports projects from files

### TestUpgrade174
- Main test class with pytest integration
- Manages test lifecycle and data
- Provides validation and verification methods

## Best Practices

1. **Configuration Management**: Keep configurations in version control
2. **Data Isolation**: Use unique namespaces and names for test data
3. **Cleanup**: Always run cleanup after tests to avoid resource accumulation
4. **Validation**: Verify configuration before running tests
5. **Documentation**: Document any custom configurations or requirements
6. **Scalability**: Start with small numbers and increase gradually for large-scale testing
7. **Security**: Use strong passwords and secure GitLab configurations

## Troubleshooting

### Common Issues

1. **Kubernetes Connection**: Check kubectl configuration and cluster access
2. **GitLab Operator**: Ensure GitLab Operator is installed and running
3. **Configuration Errors**: Check YAML syntax and required fields
4. **File Not Found**: Ensure import file paths are correct
5. **Permission Issues**: Verify GitLab access permissions
6. **Network Issues**: Check connectivity to GitLab instance

### Debug Mode

Enable debug output by setting environment variables:
```bash
export PYTHONPATH=.
python -m pytest upgrade_gitlab_test.py -v -s
```

### SSL Warnings

The framework automatically suppresses SSL warnings for development environments. For production, configure proper SSL certificates.

## Contributing

When adding new test scenarios:

1. Update configuration schema if needed
2. Add validation for new configuration sections
3. Update documentation
4. Test with different configuration variations
5. Follow the existing test structure and naming conventions
