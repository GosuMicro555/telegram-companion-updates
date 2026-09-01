export namespace runtimeconfig {

	export class Store {


	    static createFrom(source: any = {}) {
	        return new Store(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);

	    }
	}

}

export namespace usecase {

	export class AutomationController {


	    static createFrom(source: any = {}) {
	        return new AutomationController(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);

	    }
	}

}

export namespace wails {

	export class AIImportDTO {
	    id: string;
	    fileName: string;
	    filePath: string;
	    status: string;
	    recordCount: number;
	    importedAt: string;
	    error: string;

	    static createFrom(source: any = {}) {
	        return new AIImportDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.fileName = source["fileName"];
	        this.filePath = source["filePath"];
	        this.status = source["status"];
	        this.recordCount = source["recordCount"];
	        this.importedAt = source["importedAt"];
	        this.error = source["error"];
	    }
	}
	export class AIImportRequestDTO {
	    path: string;
	    sha256: string;
	    rightsConfirmed: boolean;
	    profileId: string;

	    static createFrom(source: any = {}) {
	        return new AIImportRequestDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.sha256 = source["sha256"];
	        this.rightsConfirmed = source["rightsConfirmed"];
	        this.profileId = source["profileId"];
	    }
	}
	export class AccountDTO {
	    id: string;
	    displayName: string;
	    username: string;
	    phoneMasked: string;
	    role: string;
	    status: string;
	    proxy: string;
	    proxyWarning: string;
	    proxyProfileId: string;
	    proxyRouteName: string;
	    proxyRouteState: string;
	    proxyRouteUsage: number;
	    proxyRouteCapacity: number;
	    publicRepliesSent: number;
	    privateMessagesSent: number;
	    nextDelivery: string;
	    privateMessagesClosed: number;
	    lastActivity: string;
	    legacyPauseReviewRequired: boolean;
	    revision: number;

	    static createFrom(source: any = {}) {
	        return new AccountDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.displayName = source["displayName"];
	        this.username = source["username"];
	        this.phoneMasked = source["phoneMasked"];
	        this.role = source["role"];
	        this.status = source["status"];
	        this.proxy = source["proxy"];
	        this.proxyWarning = source["proxyWarning"];
	        this.proxyProfileId = source["proxyProfileId"];
	        this.proxyRouteName = source["proxyRouteName"];
	        this.proxyRouteState = source["proxyRouteState"];
	        this.proxyRouteUsage = source["proxyRouteUsage"];
	        this.proxyRouteCapacity = source["proxyRouteCapacity"];
	        this.publicRepliesSent = source["publicRepliesSent"];
	        this.privateMessagesSent = source["privateMessagesSent"];
	        this.nextDelivery = source["nextDelivery"];
	        this.privateMessagesClosed = source["privateMessagesClosed"];
	        this.lastActivity = source["lastActivity"];
	        this.legacyPauseReviewRequired = source["legacyPauseReviewRequired"];
	        this.revision = source["revision"];
	    }
	}
	export class AccountRestDTO {
	    accountID: string;
	    accountTitle: string;
	    channelID: string;
	    channelTitle: string;
	    catalog: string;
	    status: string;
	    startedAt: string;
	    until: string;
	    durationHours: number;

	    static createFrom(source: any = {}) {
	        return new AccountRestDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.accountID = source["accountID"];
	        this.accountTitle = source["accountTitle"];
	        this.channelID = source["channelID"];
	        this.channelTitle = source["channelTitle"];
	        this.catalog = source["catalog"];
	        this.status = source["status"];
	        this.startedAt = source["startedAt"];
	        this.until = source["until"];
	        this.durationHours = source["durationHours"];
	    }
	}
	export class ActivationStatusDTO {
	    mode: string;
	    state: string;
	    machineID?: string;
	    errorCode?: string;

	    static createFrom(source: any = {}) {
	        return new ActivationStatusDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.state = source["state"];
	        this.machineID = source["machineID"];
	        this.errorCode = source["errorCode"];
	    }
	}
	export class AnalysisCandidateDTO {
	    runId: string;
	    normalizedValue: string;
	    displayValue: string;
	    kind: string;
	    frequency: number;
	    sourceDiversity: number;
	    score: number;
	    source: string;
	    moderationState: string;

	    static createFrom(source: any = {}) {
	        return new AnalysisCandidateDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runId = source["runId"];
	        this.normalizedValue = source["normalizedValue"];
	        this.displayValue = source["displayValue"];
	        this.kind = source["kind"];
	        this.frequency = source["frequency"];
	        this.sourceDiversity = source["sourceDiversity"];
	        this.score = source["score"];
	        this.source = source["source"];
	        this.moderationState = source["moderationState"];
	    }
	}
	export class AnalysisCandidateRefDTO {
	    runId: string;
	    normalizedValue: string;
	    kind: string;

	    static createFrom(source: any = {}) {
	        return new AnalysisCandidateRefDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runId = source["runId"];
	        this.normalizedValue = source["normalizedValue"];
	        this.kind = source["kind"];
	    }
	}
	export class AnalysisRequestDTO {
	    sourceScope: string;
	    profileId: string;
	    sourceTopics: string[];

	    static createFrom(source: any = {}) {
	        return new AnalysisRequestDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceScope = source["sourceScope"];
	        this.profileId = source["profileId"];
	        this.sourceTopics = source["sourceTopics"];
	    }
	}
	export class AnalysisResultsDTO {
	    runId: string;
	    status: string;
	    generalPhrases: AnalysisCandidateDTO[];
	    generalWords: AnalysisCandidateDTO[];
	    profileCandidates: AnalysisCandidateDTO[];

	    static createFrom(source: any = {}) {
	        return new AnalysisResultsDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runId = source["runId"];
	        this.status = source["status"];
	        this.generalPhrases = this.convertValues(source["generalPhrases"], AnalysisCandidateDTO);
	        this.generalWords = this.convertValues(source["generalWords"], AnalysisCandidateDTO);
	        this.profileCandidates = this.convertValues(source["profileCandidates"], AnalysisCandidateDTO);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AnalysisStatusDTO {
	    status: string;
	    operation: string;
	    progress: number;
	    error: string;
	    startedAt: string;
	    completedAt: string;

	    static createFrom(source: any = {}) {
	        return new AnalysisStatusDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.operation = source["operation"];
	        this.progress = source["progress"];
	        this.error = source["error"];
	        this.startedAt = source["startedAt"];
	        this.completedAt = source["completedAt"];
	    }
	}
	export class AnalyticsTotalsDTO {
	    messages: number;
	    canonicalWords: number;
	    forms: number;
	    groups: number;
	    keywordDbBytes: number;

	    static createFrom(source: any = {}) {
	        return new AnalyticsTotalsDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.messages = source["messages"];
	        this.canonicalWords = source["canonicalWords"];
	        this.forms = source["forms"];
	        this.groups = source["groups"];
	        this.keywordDbBytes = source["keywordDbBytes"];
	    }
	}
	export class AnalyticsRunMetricsDTO {
	    newMessages: number;
	    extractedWords: number;
	    newCanonicalWords: number;
	    processedGroups: number;
	    durationMillis: number;
	    errors: string[];

	    static createFrom(source: any = {}) {
	        return new AnalyticsRunMetricsDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.newMessages = source["newMessages"];
	        this.extractedWords = source["extractedWords"];
	        this.newCanonicalWords = source["newCanonicalWords"];
	        this.processedGroups = source["processedGroups"];
	        this.durationMillis = source["durationMillis"];
	        this.errors = source["errors"];
	    }
	}
	export class AnalyticsCollectionStatusDTO {
	    running: boolean;
	    collecting: boolean;
	    state: string;
	    intervalMinutes: number;
	    lastRunAt: string;
	    nextRunAt: string;
	    lastError: string;
	    allTimeMessages: number;
	    allTimeKeywords: number;
	    allTimeGroups: number;
	    latestCollection: AnalyticsRunMetricsDTO;
	    totals: AnalyticsTotalsDTO;

	    static createFrom(source: any = {}) {
	        return new AnalyticsCollectionStatusDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.collecting = source["collecting"];
	        this.state = source["state"];
	        this.intervalMinutes = source["intervalMinutes"];
	        this.lastRunAt = source["lastRunAt"];
	        this.nextRunAt = source["nextRunAt"];
	        this.lastError = source["lastError"];
	        this.allTimeMessages = source["allTimeMessages"];
	        this.allTimeKeywords = source["allTimeKeywords"];
	        this.allTimeGroups = source["allTimeGroups"];
	        this.latestCollection = this.convertValues(source["latestCollection"], AnalyticsRunMetricsDTO);
	        this.totals = this.convertValues(source["totals"], AnalyticsTotalsDTO);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AnalyticsMetricsDTO {
	    canonicalCount: number;
	    formCount: number;
	    messageCount: number;
	    groupCount: number;
	    logicalKeywordBytes: number;

	    static createFrom(source: any = {}) {
	        return new AnalyticsMetricsDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.canonicalCount = source["canonicalCount"];
	        this.formCount = source["formCount"];
	        this.messageCount = source["messageCount"];
	        this.groupCount = source["groupCount"];
	        this.logicalKeywordBytes = source["logicalKeywordBytes"];
	    }
	}
	export class AnalyticsRunDTO {
	    id: string;
	    startedAt: string;
	    finishedAt: string;
	    newMessages: number;
	    extractedWords: number;
	    newCanonicals: number;
	    processedGroups: number;
	    durationMillis: number;
	    errors: string[];

	    static createFrom(source: any = {}) {
	        return new AnalyticsRunDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.startedAt = source["startedAt"];
	        this.finishedAt = source["finishedAt"];
	        this.newMessages = source["newMessages"];
	        this.extractedWords = source["extractedWords"];
	        this.newCanonicals = source["newCanonicals"];
	        this.processedGroups = source["processedGroups"];
	        this.durationMillis = source["durationMillis"];
	        this.errors = source["errors"];
	    }
	}

	export class AnalyticsSettingsDTO {
	    enabled: boolean;
	    intervalMinutes: number;

	    static createFrom(source: any = {}) {
	        return new AnalyticsSettingsDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.intervalMinutes = source["intervalMinutes"];
	    }
	}
	export class AnalyticsTableColumnPreferenceDTO {
	    key: string;
	    width: number;
	    visible: boolean;

	    static createFrom(source: any = {}) {
	        return new AnalyticsTableColumnPreferenceDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.width = source["width"];
	        this.visible = source["visible"];
	    }
	}
	export class AnalyticsTablePreferenceDTO {
	    tab: string;
	    columns: AnalyticsTableColumnPreferenceDTO[];
	    sortBy: string;
	    sortDirection: string;
	    pageSize: number;

	    static createFrom(source: any = {}) {
	        return new AnalyticsTablePreferenceDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tab = source["tab"];
	        this.columns = this.convertValues(source["columns"], AnalyticsTableColumnPreferenceDTO);
	        this.sortBy = source["sortBy"];
	        this.sortDirection = source["sortDirection"];
	        this.pageSize = source["pageSize"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class AppSettingsDTO {
	    repliesPerMinute: number;
	    minIntervalSeconds: number;
	    joinIntervalMinMinutes: number;
	    joinIntervalMaxMinutes: number;
	    joinIntervalEnabled: boolean;
	    groupRestHours: number;
	    groupRestEnabled: boolean;
	    directMessages: boolean;
	    proxy: string;
	    revision: number;

	    static createFrom(source: any = {}) {
	        return new AppSettingsDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.repliesPerMinute = source["repliesPerMinute"];
	        this.minIntervalSeconds = source["minIntervalSeconds"];
	        this.joinIntervalMinMinutes = source["joinIntervalMinMinutes"];
	        this.joinIntervalMaxMinutes = source["joinIntervalMaxMinutes"];
	        this.joinIntervalEnabled = source["joinIntervalEnabled"];
	        this.groupRestHours = source["groupRestHours"];
	        this.groupRestEnabled = source["groupRestEnabled"];
	        this.directMessages = source["directMessages"];
	        this.proxy = source["proxy"];
	        this.revision = source["revision"];
	    }
	}
	export class BackupDTO {
	    id: string;
	    archivePath: string;
	    kind: string;
	    sizeBytes: number;
	    sha256: string;
	    status: string;
	    createdAt: string;
	    verifiedAt: string;
	    error: string;

	    static createFrom(source: any = {}) {
	        return new BackupDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.archivePath = source["archivePath"];
	        this.kind = source["kind"];
	        this.sizeBytes = source["sizeBytes"];
	        this.sha256 = source["sha256"];
	        this.status = source["status"];
	        this.createdAt = source["createdAt"];
	        this.verifiedAt = source["verifiedAt"];
	        this.error = source["error"];
	    }
	}
	export class BackupRuntime {
	    // Go type: usecase
	    Service?: any;
	    Runner: any;

	    static createFrom(source: any = {}) {
	        return new BackupRuntime(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.Service = this.convertValues(source["Service"], null);
	        this.Runner = source["Runner"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CanonicalAnalysisResultDTO {
	    messageCount: number;
	    keywordCount: number;

	    static createFrom(source: any = {}) {
	        return new CanonicalAnalysisResultDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.messageCount = source["messageCount"];
	        this.keywordCount = source["keywordCount"];
	    }
	}
	export class CanonicalBulkImportResultDTO {
	    added: number;
	    updated: number;
	    skipped: number;

	    static createFrom(source: any = {}) {
	        return new CanonicalBulkImportResultDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.added = source["added"];
	        this.updated = source["updated"];
	        this.skipped = source["skipped"];
	    }
	}
	export class CanonicalFormDTO {
	    value: string;
	    frequency: number;

	    static createFrom(source: any = {}) {
	        return new CanonicalFormDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.value = source["value"];
	        this.frequency = source["frequency"];
	    }
	}
	export class CanonicalImportEntryDTO {
	    canonical: string;
	    forms: string[];

	    static createFrom(source: any = {}) {
	        return new CanonicalImportEntryDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.canonical = source["canonical"];
	        this.forms = source["forms"];
	    }
	}
	export class CanonicalKeywordDTO {
	    id: string;
	    canonical: string;
	    language: string;
	    class: string;
	    frequency: number;
	    frequencyDelta: number;
	    messageCount: number;
	    lastSeen: string;
	    forms: CanonicalFormDTO[];
	    triggerActive: boolean;

	    static createFrom(source: any = {}) {
	        return new CanonicalKeywordDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.canonical = source["canonical"];
	        this.language = source["language"];
	        this.class = source["class"];
	        this.frequency = source["frequency"];
	        this.frequencyDelta = source["frequencyDelta"];
	        this.messageCount = source["messageCount"];
	        this.lastSeen = source["lastSeen"];
	        this.forms = this.convertValues(source["forms"], CanonicalFormDTO);
	        this.triggerActive = source["triggerActive"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class CatalogEntryDTO {
	    id: string;
	    title: string;
	    link: string;
	    topic: string;
	    status: string;
	    messageCount: number;
	    sentCount: number;
	    lastActivity: string;
	    active: boolean;
	    member: number;
	    pendingApproval: number;
	    joining: number;
	    leaving: number;
	    failed: number;
	    joinNotBefore: string;
	    planned: boolean;
	    revision: number;

	    static createFrom(source: any = {}) {
	        return new CatalogEntryDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.link = source["link"];
	        this.topic = source["topic"];
	        this.status = source["status"];
	        this.messageCount = source["messageCount"];
	        this.sentCount = source["sentCount"];
	        this.lastActivity = source["lastActivity"];
	        this.active = source["active"];
	        this.member = source["member"];
	        this.pendingApproval = source["pendingApproval"];
	        this.joining = source["joining"];
	        this.leaving = source["leaving"];
	        this.failed = source["failed"];
	        this.joinNotBefore = source["joinNotBefore"];
	        this.planned = source["planned"];
	        this.revision = source["revision"];
	    }
	}
	export class ChannelModerationAccountDTO {
	    title: string;
	    role: string;
	    requestSubmittedAt: string;
	    joinedAt: string;
	    durationSeconds: number;
	    status: string;

	    static createFrom(source: any = {}) {
	        return new ChannelModerationAccountDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	        this.role = source["role"];
	        this.requestSubmittedAt = source["requestSubmittedAt"];
	        this.joinedAt = source["joinedAt"];
	        this.durationSeconds = source["durationSeconds"];
	        this.status = source["status"];
	    }
	}
	export class ChannelModerationDTO {
	    catalog: string;
	    channelID: string;
	    title: string;
	    link: string;
	    topic: string;
	    applications: number;
	    joined: number;
	    pending: number;
	    status: string;
	    firstRequestAt: string;
	    durationSeconds: number;
	    accounts: ChannelModerationAccountDTO[];

	    static createFrom(source: any = {}) {
	        return new ChannelModerationDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.catalog = source["catalog"];
	        this.channelID = source["channelID"];
	        this.title = source["title"];
	        this.link = source["link"];
	        this.topic = source["topic"];
	        this.applications = source["applications"];
	        this.joined = source["joined"];
	        this.pending = source["pending"];
	        this.status = source["status"];
	        this.firstRequestAt = source["firstRequestAt"];
	        this.durationSeconds = source["durationSeconds"];
	        this.accounts = this.convertValues(source["accounts"], ChannelModerationAccountDTO);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class DashboardDTO {
	    running: boolean;
	    locale: string;
	    channelCount: number;
	    accountCount: number;
	    lastStatus: string;

	    static createFrom(source: any = {}) {
	        return new DashboardDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.locale = source["locale"];
	        this.channelCount = source["channelCount"];
	        this.accountCount = source["accountCount"];
	        this.lastStatus = source["lastStatus"];
	    }
	}
	export class DesktopStartupStatusDTO {
	    mode: string;
	    errorCode?: string;
	    restarting?: boolean;

	    static createFrom(source: any = {}) {
	        return new DesktopStartupStatusDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.errorCode = source["errorCode"];
	        this.restarting = source["restarting"];
	    }
	}
	export class KeywordSettingsDTO {
	    keywords: string[];
	    minusKeywords: string[];
	    sharedReply: string;
	    privateReply: string;
	    deliveryMode: string;
	    directMessageKeywords: string[];
	    revision: number;

	    static createFrom(source: any = {}) {
	        return new KeywordSettingsDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.keywords = source["keywords"];
	        this.minusKeywords = source["minusKeywords"];
	        this.sharedReply = source["sharedReply"];
	        this.privateReply = source["privateReply"];
	        this.deliveryMode = source["deliveryMode"];
	        this.directMessageKeywords = source["directMessageKeywords"];
	        this.revision = source["revision"];
	    }
	}
	export class LiveDeliveryExportRequestDTO {
	    from: string;
	    to: string;
	    sortBy: string;
	    sortDirection: string;
	    columns: string[];
	    locale: string;
	    timeZone: string;

	    static createFrom(source: any = {}) {
	        return new LiveDeliveryExportRequestDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.sortBy = source["sortBy"];
	        this.sortDirection = source["sortDirection"];
	        this.columns = source["columns"];
	        this.locale = source["locale"];
	        this.timeZone = source["timeZone"];
	    }
	}
	export class LiveDeliveryRowDTO {
	    id: string;
	    sourceMessage: string;
	    triggerCanonicalID?: string;
	    triggerSnapshot: string;
	    triggeredAt: string;
	    deliveryType: string;
	    accountTitleSnapshot: string;
	    finalStatus: string;
	    errorCode: string;
	    finalizedAt: string;

	    static createFrom(source: any = {}) {
	        return new LiveDeliveryRowDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.sourceMessage = source["sourceMessage"];
	        this.triggerCanonicalID = source["triggerCanonicalID"];
	        this.triggerSnapshot = source["triggerSnapshot"];
	        this.triggeredAt = source["triggeredAt"];
	        this.deliveryType = source["deliveryType"];
	        this.accountTitleSnapshot = source["accountTitleSnapshot"];
	        this.finalStatus = source["finalStatus"];
	        this.errorCode = source["errorCode"];
	        this.finalizedAt = source["finalizedAt"];
	    }
	}
	export class LiveDeliveryPageDTO {
	    rows: LiveDeliveryRowDTO[];
	    total: number;
	    databaseBytes: number;
	    refreshedAt: string;

	    static createFrom(source: any = {}) {
	        return new LiveDeliveryPageDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rows = this.convertValues(source["rows"], LiveDeliveryRowDTO);
	        this.total = source["total"];
	        this.databaseBytes = source["databaseBytes"];
	        this.refreshedAt = source["refreshedAt"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LiveDeliveryQueryDTO {
	    from: string;
	    to: string;
	    page: number;
	    pageSize: number;
	    sortBy: string;
	    sortDirection: string;

	    static createFrom(source: any = {}) {
	        return new LiveDeliveryQueryDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.page = source["page"];
	        this.pageSize = source["pageSize"];
	        this.sortBy = source["sortBy"];
	        this.sortDirection = source["sortDirection"];
	    }
	}

	export class ManagedChannelDTO {
	    id: string;
	    title: string;
	    link: string;
	    status: string;
	    members: string;
	    sent: number;
	    lastActivity: string;
	    active: boolean;
	    revision: number;

	    static createFrom(source: any = {}) {
	        return new ManagedChannelDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.link = source["link"];
	        this.status = source["status"];
	        this.members = source["members"];
	        this.sent = source["sent"];
	        this.lastActivity = source["lastActivity"];
	        this.active = source["active"];
	        this.revision = source["revision"];
	    }
	}
	export class ProxyProfileDTO {
	    id: string;
	    name: string;
	    protocol: string;
	    maskedEndpoint: string;
	    endpoint: string;
	    state: string;
	    usage: number;
	    capacity: number;
	    error: string;
	    lastError: string;
	    enabled: boolean;
	    passwordConfigured: boolean;

	    static createFrom(source: any = {}) {
	        return new ProxyProfileDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.protocol = source["protocol"];
	        this.maskedEndpoint = source["maskedEndpoint"];
	        this.endpoint = source["endpoint"];
	        this.state = source["state"];
	        this.usage = source["usage"];
	        this.capacity = source["capacity"];
	        this.error = source["error"];
	        this.lastError = source["lastError"];
	        this.enabled = source["enabled"];
	        this.passwordConfigured = source["passwordConfigured"];
	    }
	}
	export class ProxyProfileInputDTO {
	    id: string;
	    name: string;
	    protocol: string;
	    host: string;
	    port: number;
	    username: string;
	    password: string;
	    clearPassword: boolean;
	    enabled: boolean;

	    static createFrom(source: any = {}) {
	        return new ProxyProfileInputDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.protocol = source["protocol"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.username = source["username"];
	        this.password = source["password"];
	        this.clearPassword = source["clearPassword"];
	        this.enabled = source["enabled"];
	    }
	}
	export class ProxyStatusDTO {
	    mode: string;
	    state: string;
	    address: string;
	    transport: string;
	    lastError: string;
	    restartCount: number;
	    updatedAt: string;
	    autoRestart: boolean;

	    static createFrom(source: any = {}) {
	        return new ProxyStatusDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.state = source["state"];
	        this.address = source["address"];
	        this.transport = source["transport"];
	        this.lastError = source["lastError"];
	        this.restartCount = source["restartCount"];
	        this.updatedAt = source["updatedAt"];
	        this.autoRestart = source["autoRestart"];
	    }
	}
	export class ReplyStatisticsBucketDTO {
	    date: string;
	    replies: number;

	    static createFrom(source: any = {}) {
	        return new ReplyStatisticsBucketDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.date = source["date"];
	        this.replies = source["replies"];
	    }
	}
	export class ReplyStatisticsRowDTO {
	    id: string;
	    title: string;
	    replies: number;
	    publicReplies: number;
	    privateMessages: number;
	    privateMessagesClosed: number;
	    lastActivityAt: string;

	    static createFrom(source: any = {}) {
	        return new ReplyStatisticsRowDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.replies = source["replies"];
	        this.publicReplies = source["publicReplies"];
	        this.privateMessages = source["privateMessages"];
	        this.privateMessagesClosed = source["privateMessagesClosed"];
	        this.lastActivityAt = source["lastActivityAt"];
	    }
	}
	export class ReplyStatisticsDTO {
	    replies: number;
	    publicReplies: number;
	    privateMessages: number;
	    privateMessagesClosed: number;
	    channels: number;
	    accounts: number;
	    averageRepliesPerMinute: number;
	    timeSeries: ReplyStatisticsBucketDTO[];
	    accountRows: ReplyStatisticsRowDTO[];
	    channelRows: ReplyStatisticsRowDTO[];

	    static createFrom(source: any = {}) {
	        return new ReplyStatisticsDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.replies = source["replies"];
	        this.publicReplies = source["publicReplies"];
	        this.privateMessages = source["privateMessages"];
	        this.privateMessagesClosed = source["privateMessagesClosed"];
	        this.channels = source["channels"];
	        this.accounts = source["accounts"];
	        this.averageRepliesPerMinute = source["averageRepliesPerMinute"];
	        this.timeSeries = this.convertValues(source["timeSeries"], ReplyStatisticsBucketDTO);
	        this.accountRows = this.convertValues(source["accountRows"], ReplyStatisticsRowDTO);
	        this.channelRows = this.convertValues(source["channelRows"], ReplyStatisticsRowDTO);
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

	export class ScheduledDMDraftDTO {
	    id: string;
	    messageText: string;
	    recipients: string[];
	    accountIds: string[];
	    startAt: string;
	    recurrence: string;
	    maxRuns: number;

	    static createFrom(source: any = {}) {
	        return new ScheduledDMDraftDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.messageText = source["messageText"];
	        this.recipients = source["recipients"];
	        this.accountIds = source["accountIds"];
	        this.startAt = source["startAt"];
	        this.recurrence = source["recurrence"];
	        this.maxRuns = source["maxRuns"];
	    }
	}
	export class ScheduledDMRecipientDTO {
	    username: string;
	    accountId: string;
	    status: string;
	    lastError: string;
	    lastCheckedAt: string;

	    static createFrom(source: any = {}) {
	        return new ScheduledDMRecipientDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.username = source["username"];
	        this.accountId = source["accountId"];
	        this.status = source["status"];
	        this.lastError = source["lastError"];
	        this.lastCheckedAt = source["lastCheckedAt"];
	    }
	}
	export class ScheduledDMTaskDTO {
	    id: string;
	    messageText: string;
	    status: string;
	    startAt: string;
	    recurrence: string;
	    maxRuns: number;
	    completedRuns: number;
	    nextRunAt: string;
	    recipients: ScheduledDMRecipientDTO[];
	    accountIds: string[];
	    createdAt: string;
	    updatedAt: string;

	    static createFrom(source: any = {}) {
	        return new ScheduledDMTaskDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.messageText = source["messageText"];
	        this.status = source["status"];
	        this.startAt = source["startAt"];
	        this.recurrence = source["recurrence"];
	        this.maxRuns = source["maxRuns"];
	        this.completedRuns = source["completedRuns"];
	        this.nextRunAt = source["nextRunAt"];
	        this.recipients = this.convertValues(source["recipients"], ScheduledDMRecipientDTO);
	        this.accountIds = source["accountIds"];
	        this.createdAt = source["createdAt"];
	        this.updatedAt = source["updatedAt"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class StorageMetricsDTO {
	    rowCount: number;
	    databaseBytes: number;
	    oldestTimestamp: string;
	    newestTimestamp: string;
	    pausedForLowDisk: boolean;
	    lastRefresh: string;

	    static createFrom(source: any = {}) {
	        return new StorageMetricsDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.rowCount = source["rowCount"];
	        this.databaseBytes = source["databaseBytes"];
	        this.oldestTimestamp = source["oldestTimestamp"];
	        this.newestTimestamp = source["newestTimestamp"];
	        this.pausedForLowDisk = source["pausedForLowDisk"];
	        this.lastRefresh = source["lastRefresh"];
	    }
	}
	export class TopicComparisonRequestDTO {
	    sourceScope: string;
	    profileId: string;
	    topics: string[];

	    static createFrom(source: any = {}) {
	        return new TopicComparisonRequestDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sourceScope = source["sourceScope"];
	        this.profileId = source["profileId"];
	        this.topics = source["topics"];
	    }
	}
	export class TopicSummaryDTO {
	    topic: string;
	    count: number;
	    distinctCandidateCount: number;
	    topPhrases: AnalysisCandidateDTO[];
	    relativeShare: number;

	    static createFrom(source: any = {}) {
	        return new TopicSummaryDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.topic = source["topic"];
	        this.count = source["count"];
	        this.distinctCandidateCount = source["distinctCandidateCount"];
	        this.topPhrases = this.convertValues(source["topPhrases"], AnalysisCandidateDTO);
	        this.relativeShare = source["relativeShare"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class UpdateStatusDTO {
	    state: string;
	    retryState?: string;
	    version?: string;
	    progress?: number;
	    errorCode?: string;

	    static createFrom(source: any = {}) {
	        return new UpdateStatusDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.retryState = source["retryState"];
	        this.version = source["version"];
	        this.progress = source["progress"];
	        this.errorCode = source["errorCode"];
	    }
	}

}
