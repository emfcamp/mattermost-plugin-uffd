import NodeEnvironment from 'jest-environment-node';

export default class CustomEnvironment extends NodeEnvironment {
    async setup() {
        await super.setup();
        this.global.ReadableStream = ReadableStream;
        this.global.Blob = Blob;
        this.global.File = File;
        this.global.MessagePort = MessagePort;
        this.global.DOMException = DOMException;
    }
}
