export namespace api {
	
	export class ConflictDetail {
	    id: string;
	    base: number[];
	    ours: number[];
	    theirs: number[];
	
	    static createFrom(source: any = {}) {
	        return new ConflictDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.base = source["base"];
	        this.ours = source["ours"];
	        this.theirs = source["theirs"];
	    }
	}
	export class EntryView {
	    id: string;
	    group_id: string;
	    plaintext: number[];
	    seq: number;
	    key_version: number;
	    updated_at: number;
	    deleted: boolean;
	    conflict_of?: string;
	    dirty?: boolean;
	    archived?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new EntryView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.group_id = source["group_id"];
	        this.plaintext = source["plaintext"];
	        this.seq = source["seq"];
	        this.key_version = source["key_version"];
	        this.updated_at = source["updated_at"];
	        this.deleted = source["deleted"];
	        this.conflict_of = source["conflict_of"];
	        this.dirty = source["dirty"];
	        this.archived = source["archived"];
	    }
	}
	export class GroupSyncState {
	    id: string;
	    name: string;
	    key_version: number;
	    pending_rekey: boolean;
	    archived: boolean;
	
	    static createFrom(source: any = {}) {
	        return new GroupSyncState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.key_version = source["key_version"];
	        this.pending_rekey = source["pending_rekey"];
	        this.archived = source["archived"];
	    }
	}
	export class PutEntryRequest {
	    id: string;
	    group_id: string;
	    plaintext: number[];
	
	    static createFrom(source: any = {}) {
	        return new PutEntryRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.group_id = source["group_id"];
	        this.plaintext = source["plaintext"];
	    }
	}
	export class SyncStatus {
	    phase: string;
	    server_seq: number;
	    last_seq: number;
	    last_pull_at: number;
	    connected: boolean;
	    groups: GroupSyncState[];
	    pending_entries: number;
	    bad_entries: number;
	    dirty_count: number;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new SyncStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.phase = source["phase"];
	        this.server_seq = source["server_seq"];
	        this.last_seq = source["last_seq"];
	        this.last_pull_at = source["last_pull_at"];
	        this.connected = source["connected"];
	        this.groups = this.convertValues(source["groups"], GroupSyncState);
	        this.pending_entries = source["pending_entries"];
	        this.bad_entries = source["bad_entries"];
	        this.dirty_count = source["dirty_count"];
	        this.error = source["error"];
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
	export class UnlockResult {
	    user_id: string;
	    device_id: string;
	    device_name: string;
	    groups: number;
	    need_register: boolean;
	
	    static createFrom(source: any = {}) {
	        return new UnlockResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.user_id = source["user_id"];
	        this.device_id = source["device_id"];
	        this.device_name = source["device_name"];
	        this.groups = source["groups"];
	        this.need_register = source["need_register"];
	    }
	}

}

export namespace proto {
	
	export class AdminDevice {
	    device_id: string;
	    user_id: string;
	    user_name: string;
	    name: string;
	    hostname: string;
	    ip: string;
	    online: boolean;
	    last_seen: number;
	    status: string;
	
	    static createFrom(source: any = {}) {
	        return new AdminDevice(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.device_id = source["device_id"];
	        this.user_id = source["user_id"];
	        this.user_name = source["user_name"];
	        this.name = source["name"];
	        this.hostname = source["hostname"];
	        this.ip = source["ip"];
	        this.online = source["online"];
	        this.last_seen = source["last_seen"];
	        this.status = source["status"];
	    }
	}
	export class DeviceBrief {
	    device_id: string;
	    name: string;
	    hostname: string;
	    ip: string;
	    online: boolean;
	    last_seen: number;
	
	    static createFrom(source: any = {}) {
	        return new DeviceBrief(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.device_id = source["device_id"];
	        this.name = source["name"];
	        this.hostname = source["hostname"];
	        this.ip = source["ip"];
	        this.online = source["online"];
	        this.last_seen = source["last_seen"];
	    }
	}
	export class GroupInfo {
	    id: string;
	    name: string;
	    key_version: number;
	    pending_rekey: boolean;
	    archived: boolean;
	    archived_at?: number;
	    member_count: number;
	    created_at: number;
	
	    static createFrom(source: any = {}) {
	        return new GroupInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.key_version = source["key_version"];
	        this.pending_rekey = source["pending_rekey"];
	        this.archived = source["archived"];
	        this.archived_at = source["archived_at"];
	        this.member_count = source["member_count"];
	        this.created_at = source["created_at"];
	    }
	}
	export class GroupMemberInfo {
	    user_id: string;
	    name: string;
	    online: boolean;
	    devices: DeviceBrief[];
	
	    static createFrom(source: any = {}) {
	        return new GroupMemberInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.user_id = source["user_id"];
	        this.name = source["name"];
	        this.online = source["online"];
	        this.devices = this.convertValues(source["devices"], DeviceBrief);
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
	export class UserInfo {
	    user_id: string;
	    username?: string;
	    name: string;
	    sm2_public_key: string;
	    status: string;
	    role?: string;
	
	    static createFrom(source: any = {}) {
	        return new UserInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.user_id = source["user_id"];
	        this.username = source["username"];
	        this.name = source["name"];
	        this.sm2_public_key = source["sm2_public_key"];
	        this.status = source["status"];
	        this.role = source["role"];
	    }
	}

}

