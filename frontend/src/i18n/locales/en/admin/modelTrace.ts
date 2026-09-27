export default {
  modelTrace: {
    title: 'Codex Model Trace',
    description: 'Self-built model fingerprint bank from random-integer distributions; detects whether the upstream actually serves the requested model',

    // Settings
    settings: 'Detection Settings',
    models: 'Candidate Models',
    modelsHelp: 'Models included in bank building and detection (at least 2); verdicts rely on cross-model discrimination',
    bankRepeats: 'Bank Samples per Model',
    bankRepeatsHelp: 'Answers collected per model, rotating across accounts (min 8, 12+ recommended)',
    detectRepeats: 'Answers per Detection',
    concurrency: 'Concurrency',
    probeTimeout: 'Probe Timeout (s)',
    requestGap: 'Request Gap (ms)',
    save: 'Save Settings',
    saving: 'Saving...',
    needTwoModels: 'Select at least 2 candidate models',

    // Accounts
    accounts: 'Probe Accounts',
    accountsHelp: 'OpenAI OAuth accounts used for probing; requests egress via each account\'s own proxy',
    selectGroup: 'Select Group',
    allGroups: 'All Groups',
    selectedCount: '{count} selected',

    // Run
    run: 'Run Task',
    runHelp: 'Bank: collect samples for every candidate model and rebuild the fingerprint bank (replaces the old one); Detect: for each (account × model) pair, identify which model the upstream actually serves',
    runBank: 'Build Bank',
    runDetect: 'Detect',
    taskRunning: 'Task running',
    kindBank: 'Bank',
    kindDetect: 'Detect',
    noTask: 'No task (bank build and detect cannot run simultaneously)',

    // Bank
    bankStatus: 'Fingerprint Bank',
    bankEmpty: 'No bank built yet',
    builtAt: 'Built At',
    bankModels: 'Bank Models',
    samples: 'Bank Samples',
    totalSamples: 'Total Samples',
    calibration: 'Calibration β (by answers)',
    orderedWeight: 'Ordered Feature Weight',

    // Results
    results: 'Detection Results',
    refresh: 'Refresh',
    time: 'Time',
    kind: 'Kind',
    account: 'Account',
    model: 'Requested Model',
    verdict: 'Verdict',
    match: 'Match',
    matchOk: 'Match',
    mismatch: 'Mismatch',
    probability: 'Confidence',
    validRuns: 'Valid/Failed',
    avgLatency: 'Avg Latency',
    reasons: 'Reasons',
    noResults: 'No results yet',

    // Samples
    samplesCard: 'Bank Samples',
    allModels: 'All Models',
    expectedCount: 'Expected/Actual',
    latency: 'Latency',
    numbers: 'Numbers',
    deleteSamples: 'Delete Model Samples',
    confirmDelete: 'Delete all bank samples of model {model}?',
    deleted: 'Deleted {count} samples',
    noSamples: 'No samples'
  }
}
